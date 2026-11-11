package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxShortDesc = 500

const descAddEntry = `Store a new entry: a paper or a note. Insert-only: a title that already exists is rejected, and afterwards only tags and projects can change (update_links).

Before calling: run list_entries with query set to the title or the arXiv ID/DOI so the same paper is not stored twice, and call list_tags and list_projects to reuse existing values.

You do the extraction and writing; the server only stores. Write the texts to files anywhere (e.g. a temp directory) and pass their paths. The server copies them into its own directory, so never write into the store yourself and never pass the text inline. Paths must be absolute or start with ~/. The caller's files are left untouched; delete your temp files afterwards.

- content_path (required): the full text as UTF-8 Markdown, one paragraph or list item per line (no hard wrapping), no local file paths.
- summary_path (expected for papers, skip for notes): the summary in the template below, one bullet per line.
- file_path (optional): the original document (e.g. the PDF), stored as is. Its name needs a letters-and-digits extension.
- short_desc: one or two sentences saying what the entry is about, at most 500 characters.
- source: arXiv ID (e.g. arXiv:1706.03762), DOI, or URL, whenever one exists.
- tags: topics, reusing existing tags. projects: the repository directory name(s) the entry belongs to.

Use ASCII "-" for minus signs in numbers so literal search matches.

Summary template (same every time):
# <Title>
- Authors: ...
- Code: <URL or none>
- Source: <arXiv ID, DOI, or URL>

## Methods
- ...

## Benchmarks
- ...

## Results
- ...

## Limitations
- ...`

const descUpdateLinks = `Add or remove tags and projects on an existing entry; this is the only change an entry allows.
Adding a value it already has, or removing one it does not have, does nothing. Returns the resulting tags and projects.`

const descDeleteEntry = `Delete an entry with its tags, projects, and all of its stored files, including the original document. There is no undo.
To replace an entry's text or title, copy its files out first (list_entries with fields=["files"]), then delete and add it again.`

func registerWriteTools(srv *mcp.Server, s *Store) {
	mcp.AddTool(srv, &mcp.Tool{Name: "add_entry", Description: descAddEntry,
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false)}}, s.handleAddEntry)
	mcp.AddTool(srv, &mcp.Tool{Name: "update_links", Description: descUpdateLinks,
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false)}}, s.handleUpdateLinks)
	mcp.AddTool(srv, &mcp.Tool{Name: "delete_entry", Description: descDeleteEntry,
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)}}, s.handleDeleteEntry)
}

// add_entry

type AddEntryIn struct {
	Title       string   `json:"title" jsonschema:"Title of the paper or note. It identifies the entry and cannot change later."`
	ShortDesc   string   `json:"short_desc" jsonschema:"What the entry is about, at most 500 characters."`
	ContentPath string   `json:"content_path" jsonschema:"Absolute or ~/ path of the full text: UTF-8 Markdown, one paragraph per line."`
	SummaryPath string   `json:"summary_path,omitempty" jsonschema:"Absolute or ~/ path of the summary, written in the template from this tool's description."`
	FilePath    string   `json:"file_path,omitempty" jsonschema:"Absolute or ~/ path of the original document (e.g. the PDF), stored as is."`
	Source      string   `json:"source,omitempty" jsonschema:"arXiv ID, DOI, or URL."`
	Tags        []string `json:"tags,omitempty" jsonschema:"Topic tags; reuse values from list_tags."`
	Projects    []string `json:"projects,omitempty" jsonschema:"Repository directory names the entry belongs to; reuse values from list_projects."`
}

type AddEntryOut struct {
	Title    string     `json:"title"`
	Source   *string    `json:"source,omitempty"`
	Files    EntryFiles `json:"files"`
	Stats    Stats      `json:"stats"`
	Tags     []string   `json:"tags"`
	Projects []string   `json:"projects"`
}

// sourceText is one cleaned text file the caller passed to add_entry.
type sourceText struct {
	text         string
	lines, chars int
}

// loadSource reads and cleans one of the caller's text files; param names it in errors.
func loadSource(param, path string) (sourceText, error) {
	p, err := expandPath(path)
	if err != nil {
		return sourceText{}, fmt.Errorf("%s: %w", param, err)
	}
	raw, err := readSource(p)
	if err != nil {
		return sourceText{}, fmt.Errorf("%s: %w", param, err)
	}
	text, lines, chars, err := cleanText(raw)
	if err != nil {
		return sourceText{}, fmt.Errorf("%s: %s: %w", param, p, err)
	}
	return sourceText{text, lines, chars}, nil
}

func (s *Store) handleAddEntry(ctx context.Context, _ *mcp.CallToolRequest, in AddEntryIn) (*mcp.CallToolResult, AddEntryOut, error) {
	var none AddEntryOut

	// Validate and read everything before taking the write lock.
	title := strings.Join(strings.Fields(in.Title), " ")
	titleKey := normKey(title)
	if titleKey == "" {
		return nil, none, errors.New("title is empty")
	}
	shortDesc := strings.TrimSpace(in.ShortDesc)
	if shortDesc == "" {
		return nil, none, errors.New("short_desc is empty")
	}
	if n := utf8.RuneCountInString(shortDesc); n > maxShortDesc {
		return nil, none, fmt.Errorf("short_desc is %d characters; the limit is %d", n, maxShortDesc)
	}

	content, err := loadSource("content_path", in.ContentPath)
	if err != nil {
		return nil, none, err
	}
	var summary *sourceText
	if in.SummaryPath != "" {
		st, err := loadSource("summary_path", in.SummaryPath)
		if err != nil {
			return nil, none, err
		}
		summary = &st
	}
	var original, ext string
	if in.FilePath != "" {
		if original, err = expandPath(in.FilePath); err != nil {
			return nil, none, fmt.Errorf("file_path: %w", err)
		}
		if ext, err = cleanExt(filepath.Base(original)); err != nil {
			return nil, none, fmt.Errorf("file_path: %w", err)
		}
		if err := checkRegular(original); err != nil {
			return nil, none, fmt.Errorf("file_path: %w", err)
		}
	}

	e := Entry{
		Title:        title,
		TitleKey:     titleKey,
		ShortDesc:    shortDesc,
		FileKey:      fileKey(titleKey),
		ContentLines: content.lines,
		ContentChars: content.chars,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if summary != nil {
		e.SummaryLines, e.SummaryChars = &summary.lines, &summary.chars
	}
	if ext != "" {
		e.OriginalExt = &ext
	}
	if src := strings.TrimSpace(in.Source); src != "" {
		e.Source = &src
	}
	tags, projects := normKeys(in.Tags), normKeys(in.Projects)

	// Each path is recorded before it is written, so a partial write is cleaned up too.
	var written []string
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if existing, found, err := findEntry(ctx, tx, titleKey); err != nil {
			return err
		} else if found {
			return fmt.Errorf("an entry titled %q already exists; entries are insert-only "+
				"(update_links changes tags and projects; to replace it, delete_entry and add it again)", existing.Title)
		}

		dst := s.contentPath(e.FileKey)
		written = append(written, dst)
		if err := writeText(dst, content.text); err != nil {
			return fmt.Errorf("storing content: %w", err)
		}
		if summary != nil {
			dst := s.summaryPath(e.FileKey)
			written = append(written, dst)
			if err := writeText(dst, summary.text); err != nil {
				return fmt.Errorf("storing summary: %w", err)
			}
		}
		if original != "" {
			dst := s.originalPath(e.FileKey, ext)
			written = append(written, dst)
			if err := copyFile(original, dst); err != nil {
				return fmt.Errorf("storing original document: %w", err)
			}
		}
		return insertEntry(ctx, tx, e, tags, projects)
	})
	if err != nil {
		removeFiles(written)
		return nil, none, err
	}

	return nil, AddEntryOut{
		Title:    e.Title,
		Source:   e.Source,
		Files:    s.entryFiles(e),
		Stats:    e.stats(),
		Tags:     tags,
		Projects: projects,
	}, nil
}

// update_links

type UpdateLinksIn struct {
	Title          string   `json:"title" jsonschema:"Title of the entry (case-insensitive)."`
	AddTags        []string `json:"add_tags,omitempty" jsonschema:"Tags to add; reuse values from list_tags."`
	RemoveTags     []string `json:"remove_tags,omitempty" jsonschema:"Tags to remove."`
	AddProjects    []string `json:"add_projects,omitempty" jsonschema:"Projects (repository directory names) to link."`
	RemoveProjects []string `json:"remove_projects,omitempty" jsonschema:"Projects to unlink."`
}

type UpdateLinksOut struct {
	Title    string   `json:"title"`
	Tags     []string `json:"tags"`
	Projects []string `json:"projects"`
}

func (s *Store) handleUpdateLinks(ctx context.Context, _ *mcp.CallToolRequest, in UpdateLinksIn) (*mcp.CallToolResult, UpdateLinksOut, error) {
	var none UpdateLinksOut
	key := normKey(in.Title)
	if key == "" {
		return nil, none, errors.New("title is empty")
	}

	var e Entry
	var found bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if e, found, err = findEntry(ctx, tx, key); err != nil || !found {
			return err
		}
		return changeLinks(ctx, tx, e.ID, normKeys(in.AddTags), normKeys(in.RemoveTags),
			normKeys(in.AddProjects), normKeys(in.RemoveProjects))
	})
	if err != nil {
		return nil, none, err
	}
	if !found {
		return nil, none, s.notFound(ctx, in.Title)
	}

	tags, projects, err := s.loadLinks(ctx, []int64{e.ID})
	if err != nil {
		return nil, none, err
	}
	return nil, UpdateLinksOut{Title: e.Title, Tags: orEmpty(tags[e.ID]), Projects: orEmpty(projects[e.ID])}, nil
}

// delete_entry

type DeleteEntryIn struct {
	Title string `json:"title" jsonschema:"Title of the entry to delete (case-insensitive)."`
}

type DeleteEntryOut struct {
	Title string `json:"title"`
}

func (s *Store) handleDeleteEntry(ctx context.Context, _ *mcp.CallToolRequest, in DeleteEntryIn) (*mcp.CallToolResult, DeleteEntryOut, error) {
	key := normKey(in.Title)
	if key == "" {
		return nil, DeleteEntryOut{}, errors.New("title is empty")
	}

	var e Entry
	var found bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if e, found, err = findEntry(ctx, tx, key); err != nil || !found {
			return err
		}
		if err := deleteEntry(ctx, tx, e.ID); err != nil {
			return err
		}
		// Removed while the write lock is held, so a concurrent add of the same title
		// (same file key) cannot have its new files removed by this delete.
		removeFiles(s.entryPaths(e))
		return nil
	})
	if err != nil {
		return nil, DeleteEntryOut{}, err
	}
	if !found {
		return nil, DeleteEntryOut{}, s.notFound(ctx, in.Title)
	}
	return nil, DeleteEntryOut{Title: e.Title}, nil
}
