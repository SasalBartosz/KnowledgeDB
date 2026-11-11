package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const descListEntries = `List stored entries: titles plus the metadata named in fields. Start here to discover what is stored.

Filters: titles, tags, and projects (any value within one filter matches; every filter given must match) and query (case-insensitive substring of the title, short description, or source). No filter lists everything.

fields (default: titles only): short_desc, source, stats (line and character counts), tags, projects, files (absolute paths of the stored files, including the original document), created_at.

Ordered by title. Returns total; when more entries remain, continue with offset=next_offset.`

const descListTags = `List every tag in use, with the number of entries that carry it, ordered by name.
Call this before add_entry and reuse existing tags instead of creating near-duplicates.`

const descListProjects = `List every project in use, with the number of entries linked to it, ordered by name.
A project is the name of a repository directory. Call this before add_entry and reuse existing values.`

func registerReadTools(srv *mcp.Server, s *Store) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(srv, &mcp.Tool{Name: "list_entries", Description: descListEntries, Annotations: ro}, s.handleListEntries)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_tags", Description: descListTags, Annotations: ro}, s.handleListTags)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_projects", Description: descListProjects, Annotations: ro}, s.handleListProjects)
}

// filterFrom normalizes the selector lists and lower-cases the query.
func filterFrom(titles, tags, projects []string, query string) Filter {
	return Filter{
		Titles:   normKeys(titles),
		Tags:     normKeys(tags),
		Projects: normKeys(projects),
		Query:    strings.ToLower(strings.TrimSpace(query)),
	}
}

// clampLimit returns def for n <= 0 and caps n at max.
func clampLimit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	return min(n, max)
}

// orEmpty turns nil into an empty slice, so a requested list shows as [] rather than missing.
func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// list_entries

var entryFields = []string{"short_desc", "source", "stats", "tags", "projects", "files", "created_at"}

type ListEntriesIn struct {
	Fields   []string `json:"fields,omitempty" jsonschema:"Metadata to include besides the title: short_desc, source, stats, tags, projects, files, created_at."`
	Titles   []string `json:"titles,omitempty" jsonschema:"Only entries with one of these titles (case-insensitive, exact)."`
	Tags     []string `json:"tags,omitempty" jsonschema:"Only entries with at least one of these tags."`
	Projects []string `json:"projects,omitempty" jsonschema:"Only entries linked to at least one of these projects."`
	Query    string   `json:"query,omitempty" jsonschema:"Case-insensitive substring of the title, short description, or source (e.g. an arXiv ID)."`
	Limit    int      `json:"limit,omitempty" jsonschema:"Maximum entries to return; default 50, max 200."`
	Offset   int      `json:"offset,omitempty" jsonschema:"Entries to skip; pass next_offset from the previous page."`
}

// EntryInfo is one listed entry. Fields that were not requested are absent.
type EntryInfo struct {
	Title     string      `json:"title"`
	ShortDesc *string     `json:"short_desc,omitempty"`
	Source    *string     `json:"source,omitempty"`
	Stats     *Stats      `json:"stats,omitempty"`
	Tags      []string    `json:"tags,omitzero"`
	Projects  []string    `json:"projects,omitzero"`
	Files     *EntryFiles `json:"files,omitempty"`
	CreatedAt *string     `json:"created_at,omitempty"`
}

type ListEntriesOut struct {
	Entries    []EntryInfo `json:"entries"`
	Total      int         `json:"total"`
	NextOffset *int        `json:"next_offset,omitempty"`
}

func (s *Store) handleListEntries(ctx context.Context, _ *mcp.CallToolRequest, in ListEntriesIn) (*mcp.CallToolResult, ListEntriesOut, error) {
	var none ListEntriesOut
	want := map[string]bool{}
	for _, f := range in.Fields {
		f = strings.ToLower(strings.TrimSpace(f))
		if !slices.Contains(entryFields, f) {
			return nil, none, fmt.Errorf("unknown field %q; valid fields are %s", f, strings.Join(entryFields, ", "))
		}
		want[f] = true
	}
	limit := clampLimit(in.Limit, 50, 200)
	offset := max(in.Offset, 0)

	entries, total, err := s.listEntries(ctx, filterFrom(in.Titles, in.Tags, in.Projects, in.Query), limit, offset)
	if err != nil {
		return nil, none, err
	}
	var tags, projects map[int64][]string
	if want["tags"] || want["projects"] {
		ids := make([]int64, len(entries))
		for i, e := range entries {
			ids[i] = e.ID
		}
		if tags, projects, err = s.loadLinks(ctx, ids); err != nil {
			return nil, none, err
		}
	}

	out := ListEntriesOut{Entries: make([]EntryInfo, 0, len(entries)), Total: total}
	for _, e := range entries {
		info := EntryInfo{Title: e.Title}
		if want["short_desc"] {
			info.ShortDesc = &e.ShortDesc
		}
		if want["source"] {
			info.Source = e.Source
		}
		if want["stats"] {
			st := e.stats()
			info.Stats = &st
		}
		if want["tags"] {
			info.Tags = orEmpty(tags[e.ID])
		}
		if want["projects"] {
			info.Projects = orEmpty(projects[e.ID])
		}
		if want["files"] {
			f := s.entryFiles(e)
			info.Files = &f
		}
		if want["created_at"] {
			info.CreatedAt = &e.CreatedAt
		}
		out.Entries = append(out.Entries, info)
	}
	if next := offset + len(entries); next < total {
		out.NextOffset = &next
	}
	return nil, out, nil
}

// list_tags and list_projects

type ListTagsIn struct{}

type ListTagsOut struct {
	Tags []NameCount `json:"tags"`
}

func (s *Store) handleListTags(ctx context.Context, _ *mcp.CallToolRequest, _ ListTagsIn) (*mcp.CallToolResult, ListTagsOut, error) {
	tags, err := s.countValues(ctx, "entry_tags", "tag")
	if err != nil {
		return nil, ListTagsOut{}, err
	}
	return nil, ListTagsOut{Tags: tags}, nil
}

type ListProjectsIn struct{}

type ListProjectsOut struct {
	Projects []NameCount `json:"projects"`
}

func (s *Store) handleListProjects(ctx context.Context, _ *mcp.CallToolRequest, _ ListProjectsIn) (*mcp.CallToolResult, ListProjectsOut, error) {
	projects, err := s.countValues(ctx, "entry_projects", "project")
	if err != nil {
		return nil, ListProjectsOut{}, err
	}
	return nil, ListProjectsOut{Projects: projects}, nil
}
