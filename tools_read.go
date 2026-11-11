package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const descListEntries = `List stored entries: titles plus the metadata named in fields. Start here to discover what is stored.

Filters: titles, tags, and projects (any value within one filter matches; every filter given must match) and query (case-insensitive substring of the title, short description, or source). No filter lists everything.

fields (default: titles only): short_desc, source, stats (line and character counts), tags, projects, files (absolute paths of the stored files, including the original document), created_at.

Ordered by title. Returns total; when more entries remain, continue with offset=next_offset.`

const descReadEntry = `Read an entry's summary, or a range of lines of its content. Text is stored one paragraph per line, so a line is a paragraph.

part="summary": the structured summary; read it first to judge whether a paper is relevant. Notes may have none.
part="content": lines start_line..end_line (1-based, inclusive; default start_line=1, end_line=start_line+199). Locate passages with search_lines and read around them rather than reading a whole content from the start.

Stops early at 40,000 characters; a single longer line is cut and truncated is true. end_line is the last line returned: continue from end_line + 1 while end_line < total_lines.`

const descSearchLines = `Search stored text line by line and return the matching lines with their line numbers, then read around a hit with read_entry. A line is a paragraph.

mode: literal (default; plain substring) or regex (Go RE2 syntax, e.g. "warm-?up|learning rate schedule"). Case-insensitive unless case_sensitive is true. This is a text scan, not semantic search: if a term finds nothing, try synonyms or a regex.

part: both (default), summary, or content. titles, tags, and projects filter entries as in list_entries.

Entries are searched in title order, summary before content. max_per_entry (default 10) caps matches per entry across both parts. context (0-3) adds that many neighboring lines before and after each match. Lines longer than 500 characters are cut to a window around the match.

Returns at most limit matches (default 50, max 200) and stops early at 40,000 characters of text; continue with offset=next_offset. missing lists entries whose files could not be read.`

const descListTags = `List every tag in use, with the number of entries that carry it, ordered by name.
Call this before add_entry and reuse existing tags instead of creating near-duplicates.`

const descListProjects = `List every project in use, with the number of entries linked to it, ordered by name.
A project is the name of a repository directory. Call this before add_entry and reuse existing values.`

func registerReadTools(srv *mcp.Server, s *Store) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(srv, &mcp.Tool{Name: "list_entries", Description: descListEntries, Annotations: ro}, s.handleListEntries)
	mcp.AddTool(srv, &mcp.Tool{Name: "read_entry", Description: descReadEntry, Annotations: ro}, s.handleReadEntry)
	mcp.AddTool(srv, &mcp.Tool{Name: "search_lines", Description: descSearchLines, Annotations: ro}, s.handleSearchLines)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_tags", Description: descListTags, Annotations: ro}, s.handleListTags)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_projects", Description: descListProjects, Annotations: ro}, s.handleListProjects)
}

// filterFrom normalizes the selector lists and lower-cases the query.
func filterFrom(titles, tags, projects []string, query string) Filter {
	return Filter{
		Titles:   NormKeys(titles),
		Tags:     NormKeys(tags),
		Projects: NormKeys(projects),
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

	entries, total, err := s.ListEntries(ctx, filterFrom(in.Titles, in.Tags, in.Projects, in.Query), limit, offset)
	if err != nil {
		return nil, none, err
	}
	var tags, projects map[int64][]string
	if want["tags"] || want["projects"] {
		ids := make([]int64, len(entries))
		for i, e := range entries {
			ids[i] = e.ID
		}
		if tags, projects, err = s.LoadLinks(ctx, ids); err != nil {
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

// read_entry

const (
	maxTextChars     = 40000 // per read_entry or search_lines response
	defaultReadLines = 200
)

type ReadEntryIn struct {
	Title     string `json:"title" jsonschema:"Title of the entry (case-insensitive)."`
	Part      string `json:"part" jsonschema:"summary or content."`
	StartLine int    `json:"start_line,omitempty" jsonschema:"First line to return, 1-based; default 1."`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"Last line to return, inclusive; default start_line + 199."`
}

type ReadEntryOut struct {
	Title      string `json:"title"`
	Part       string `json:"part"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	TotalLines int    `json:"total_lines"`
	Text       string `json:"text"`
	Truncated  bool   `json:"truncated"`
}

func (s *Store) handleReadEntry(ctx context.Context, _ *mcp.CallToolRequest, in ReadEntryIn) (*mcp.CallToolResult, ReadEntryOut, error) {
	var none ReadEntryOut
	part := strings.ToLower(strings.TrimSpace(in.Part))
	if part != partSummary && part != partContent {
		return nil, none, fmt.Errorf(`part must be "summary" or "content", not %q`, in.Part)
	}
	key := NormKey(in.Title)
	if key == "" {
		return nil, none, errors.New("title is empty")
	}
	e, found, err := FindEntry(ctx, s.DB, key)
	if err != nil {
		return nil, none, err
	}
	if !found {
		return nil, none, s.notFound(ctx, in.Title)
	}
	if part == partSummary && !e.hasSummary() {
		return nil, none, fmt.Errorf(`entry %q has no summary; read part="content" instead, or locate passages with search_lines`, e.Title)
	}

	// total_lines comes from the file, not the row: files may have been edited by hand.
	lines, err := LoadLines(s.partPath(e, part))
	if err != nil {
		return nil, none, fmt.Errorf("reading the %s of %q: %w", part, e.Title, err)
	}
	total := len(lines)
	start := in.StartLine
	if start == 0 {
		start = 1
	}
	if start < 0 {
		return nil, none, fmt.Errorf("start_line must be at least 1, not %d", start)
	}
	if start > total {
		return nil, none, fmt.Errorf("start_line %d is past the end; the %s of %q has %d lines", start, part, e.Title, total)
	}
	end := in.EndLine
	if end == 0 {
		end = start + defaultReadLines - 1
	}
	if end < start {
		return nil, none, fmt.Errorf("end_line %d is before start_line %d", end, start)
	}

	text, last, truncated := SliceLines(lines, start, end, maxTextChars)
	return nil, ReadEntryOut{
		Title:      e.Title,
		Part:       part,
		StartLine:  start,
		EndLine:    last,
		TotalLines: total,
		Text:       text,
		Truncated:  truncated,
	}, nil
}

// search_lines

type SearchLinesIn struct {
	Pattern       string   `json:"pattern" jsonschema:"Text to find; a Go RE2 regular expression when mode is regex."`
	Mode          string   `json:"mode,omitempty" jsonschema:"literal (default; plain substring) or regex (Go RE2 syntax)."`
	CaseSensitive bool     `json:"case_sensitive,omitempty" jsonschema:"Match case exactly; default false."`
	Part          string   `json:"part,omitempty" jsonschema:"both (default), summary, or content."`
	Titles        []string `json:"titles,omitempty" jsonschema:"Only entries with one of these titles (case-insensitive, exact)."`
	Tags          []string `json:"tags,omitempty" jsonschema:"Only entries with at least one of these tags."`
	Projects      []string `json:"projects,omitempty" jsonschema:"Only entries linked to at least one of these projects."`
	Context       int      `json:"context,omitempty" jsonschema:"Lines to include before and after each match, 0-3; default 0."`
	MaxPerEntry   int      `json:"max_per_entry,omitempty" jsonschema:"Maximum matches per entry, summary and content together; default 10."`
	Limit         int      `json:"limit,omitempty" jsonschema:"Maximum matches to return; default 50, max 200."`
	Offset        int      `json:"offset,omitempty" jsonschema:"Matches to skip; pass next_offset from the previous page."`
}

type SearchLinesOut struct {
	Matches    []Match  `json:"matches"`
	NextOffset *int     `json:"next_offset,omitempty"`
	Missing    []string `json:"missing,omitempty"`
}

func (s *Store) handleSearchLines(ctx context.Context, _ *mcp.CallToolRequest, in SearchLinesIn) (*mcp.CallToolResult, SearchLinesOut, error) {
	var none SearchLinesOut
	re, err := NewMatcher(in.Pattern, in.Mode, in.CaseSensitive)
	if err != nil {
		return nil, none, err
	}
	var parts []string
	switch strings.ToLower(strings.TrimSpace(in.Part)) {
	case "", "both":
		parts = []string{partSummary, partContent}
	case partSummary:
		parts = []string{partSummary}
	case partContent:
		parts = []string{partContent}
	default:
		return nil, none, fmt.Errorf(`part must be "both", "summary", or "content", not %q`, in.Part)
	}
	contextN := clampLimit(in.Context, 0, 3)
	perEntry := in.MaxPerEntry
	if perEntry <= 0 {
		perEntry = 10
	}
	limit := clampLimit(in.Limit, 50, 200)
	offset := max(in.Offset, 0)

	// All matching entries in title order; the rows are closed before any file is read.
	entries, _, err := s.ListEntries(ctx, filterFrom(in.Titles, in.Tags, in.Projects, ""), 0, 0)
	if err != nil {
		return nil, none, err
	}

	out := SearchLinesOut{Matches: []Match{}}
	found, chars := 0, 0 // found counts every match so far, including those skipped by offset
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, none, err
		}
		budget := perEntry
		for _, part := range parts {
			if budget == 0 {
				break
			}
			if part == partSummary && !e.hasSummary() {
				continue // not missing: there is simply nothing to search
			}
			lines, err := LoadLines(s.partPath(e, part))
			if err != nil {
				log.Printf("search_lines: reading the %s of %q: %v", part, e.Title, err)
				if n := len(out.Missing); n == 0 || out.Missing[n-1] != e.Title {
					out.Missing = append(out.Missing, e.Title)
				}
				continue
			}
			ms := SearchLines(lines, re, contextN, budget)
			budget -= len(ms)
			for _, m := range ms {
				if found < offset {
					found++
					continue
				}
				if len(out.Matches) == limit || chars+m.chars() > maxTextChars {
					out.NextOffset = &found
					return nil, out, nil
				}
				m.Title, m.Part = e.Title, part
				out.Matches = append(out.Matches, m)
				chars += m.chars()
				found++
			}
		}
	}
	return nil, out, nil
}

// list_tags and list_projects

type ListTagsIn struct{}

type ListTagsOut struct {
	Tags []NameCount `json:"tags"`
}

func (s *Store) handleListTags(ctx context.Context, _ *mcp.CallToolRequest, _ ListTagsIn) (*mcp.CallToolResult, ListTagsOut, error) {
	tags, err := s.CountValues(ctx, "entry_tags", "tag")
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
	projects, err := s.CountValues(ctx, "entry_projects", "project")
	if err != nil {
		return nil, ListProjectsOut{}, err
	}
	return nil, ListProjectsOut{Projects: projects}, nil
}
