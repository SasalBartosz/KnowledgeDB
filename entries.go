package main

import (
	"context"
)

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
