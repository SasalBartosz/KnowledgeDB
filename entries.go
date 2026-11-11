package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// querier is the subset of *sql.DB and *sql.Tx that lookups need,
// so findEntry works both inside and outside a transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Entry is one row of the entries table. ID stays internal and is never exposed through MCP.
// Nil pointers are SQL NULLs: no summary, no original document, no source.
type Entry struct {
	ID           int64
	Title        string
	TitleKey     string
	ShortDesc    string
	FileKey      string
	ContentLines int
	ContentChars int
	SummaryLines *int
	SummaryChars *int
	OriginalExt  *string
	Source       *string
	CreatedAt    string
}

func (e Entry) hasSummary() bool { return e.SummaryLines != nil }

// Stats are the line and character counts stored at insert. Summary counts are absent
// when the entry has no summary.
type Stats struct {
	ContentLines int  `json:"content_lines"`
	ContentChars int  `json:"content_chars"`
	SummaryLines *int `json:"summary_lines,omitempty"`
	SummaryChars *int `json:"summary_chars,omitempty"`
}

func (e Entry) stats() Stats {
	return Stats{e.ContentLines, e.ContentChars, e.SummaryLines, e.SummaryChars}
}

// entryColumns and scanDest must list the same columns in the same order.
// Queries alias the entries table as "e".
const entryColumns = `e.id, e.title, e.title_key, e.short_desc, e.file_key, e.content_lines, e.content_chars,
	e.summary_lines, e.summary_chars, e.original_ext, e.source, e.created_at`

func (e *Entry) scanDest() []any {
	return []any{&e.ID, &e.Title, &e.TitleKey, &e.ShortDesc, &e.FileKey, &e.ContentLines, &e.ContentChars,
		&e.SummaryLines, &e.SummaryChars, &e.OriginalExt, &e.Source, &e.CreatedAt}
}

// findEntry looks up an entry by its normalized title; the bool says whether it exists.
func findEntry(ctx context.Context, q querier, titleKey string) (Entry, bool, error) {
	var e Entry
	err := q.QueryRowContext(ctx,
		"SELECT "+entryColumns+" FROM entries e WHERE e.title_key = ?", titleKey).Scan(e.scanDest()...)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	return e, true, nil
}

// notFound builds the error for a title that does not exist, suggesting up to five
// titles that contain the requested text.
func (s *Store) notFound(ctx context.Context, title string) error {
	msg := fmt.Sprintf("no entry titled %q", title)
	key := normKey(title)
	if key == "" {
		return errors.New("title is empty")
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT title FROM entries WHERE instr(title_key, ?) > 0 ORDER BY title_key LIMIT 5", key)
	if err != nil {
		return fmt.Errorf("%s (looking up similar titles failed: %v)", msg, err)
	}
	defer rows.Close()
	var similar []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			break
		}
		similar = append(similar, fmt.Sprintf("%q", t))
	}
	if len(similar) == 0 {
		return fmt.Errorf("%s; use list_entries with query to find the exact title", msg)
	}
	return fmt.Errorf("%s; did you mean: %s", msg, strings.Join(similar, ", "))
}

// insertEntry inserts the row and its tags and projects (already normalized).
func insertEntry(ctx context.Context, tx *sql.Tx, e Entry, tags, projects []string) error {
	res, err := tx.ExecContext(ctx, `INSERT INTO entries
		(title, title_key, short_desc, file_key, content_lines, content_chars,
		 summary_lines, summary_chars, original_ext, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Title, e.TitleKey, e.ShortDesc, e.FileKey, e.ContentLines, e.ContentChars,
		e.SummaryLines, e.SummaryChars, e.OriginalExt, e.Source, e.CreatedAt)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	return changeLinks(ctx, tx, id, tags, nil, projects, nil)
}

// deleteEntry deletes the row; its tags and projects cascade.
func deleteEntry(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM entries WHERE id = ?", id)
	return err
}

// changeLinks removes, then adds, tags and projects (already normalized). Adding a value that
// is already there or removing one that is not is a no-op.
func changeLinks(ctx context.Context, tx *sql.Tx, id int64, addTags, removeTags, addProjects, removeProjects []string) error {
	steps := []struct {
		query  string
		values []string
	}{
		{"DELETE FROM entry_tags WHERE entry_id = ? AND tag = ?", removeTags},
		{"DELETE FROM entry_projects WHERE entry_id = ? AND project = ?", removeProjects},
		{"INSERT OR IGNORE INTO entry_tags (entry_id, tag) VALUES (?, ?)", addTags},
		{"INSERT OR IGNORE INTO entry_projects (entry_id, project) VALUES (?, ?)", addProjects},
	}
	for _, st := range steps {
		for _, v := range st.values {
			if _, err := tx.ExecContext(ctx, st.query, id, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// NameCount is one distinct tag or project and the number of entries that use it.
type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// countValues returns every distinct value of column in table with its entry count, ordered by value.
// table and column come from constants, never from tool input.
func (s *Store) countValues(ctx context.Context, table, column string) ([]NameCount, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+column+", count(*) FROM "+table+" GROUP BY "+column+" ORDER BY "+column)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []NameCount{}
	for rows.Next() {
		var nc NameCount
		if err := rows.Scan(&nc.Name, &nc.Count); err != nil {
			return nil, err
		}
		out = append(out, nc)
	}
	return out, rows.Err()
}
