package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

// seedStore opens a store in a temp directory with three entries:
//
//	Alpha  tags ml, nlp  projects p1      source arXiv:1706.03762  desc "about transformers"
//	Beta   tags ml       projects p2      no source                desc "vision models"
//	Gamma  no tags       projects p1, p2  source doi:10.1/xyz      desc "notes"
func seedStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	seed := []struct {
		title, desc, source string
		tags, projects      []string
	}{
		{"Gamma", "notes", "doi:10.1/xyz", nil, []string{"p1", "p2"}},
		{"Alpha", "about transformers", "arXiv:1706.03762", []string{"ml", "nlp"}, []string{"p1"}},
		{"Beta", "vision models", "", []string{"ml"}, []string{"p2"}},
	}
	ctx := context.Background()
	for _, e := range seed {
		row := Entry{Title: e.title, TitleKey: NormKey(e.title), ShortDesc: e.desc, FileKey: FileKey(NormKey(e.title)),
			ContentLines: 1, ContentChars: 2, CreatedAt: "2026-11-11T00:00:00Z"}
		if e.source != "" {
			row.Source = &e.source
		}
		err := s.InTx(ctx, func(tx *sql.Tx) error { return InsertEntry(ctx, tx, row, e.tags, e.projects) })
		if err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func titlesOf(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Title)
	}
	return out
}

func TestWhereClause(t *testing.T) {
	s := seedStore(t)
	tests := []struct {
		name string
		f    Filter
		want []string
	}{
		{"no filter", Filter{}, []string{"Alpha", "Beta", "Gamma"}},
		{"titles", Filter{Titles: []string{"gamma", "alpha"}}, []string{"Alpha", "Gamma"}},
		{"unknown title", Filter{Titles: []string{"delta"}}, nil},
		{"one tag", Filter{Tags: []string{"nlp"}}, []string{"Alpha"}},
		{"tags are ORed", Filter{Tags: []string{"nlp", "ml"}}, []string{"Alpha", "Beta"}},
		{"projects", Filter{Projects: []string{"p1"}}, []string{"Alpha", "Gamma"}},
		{"tags AND projects", Filter{Tags: []string{"ml"}, Projects: []string{"p2"}}, []string{"Beta"}},
		{"titles AND tags, no match", Filter{Titles: []string{"beta"}, Tags: []string{"nlp"}}, nil},
		{"query on title", Filter{Query: "amm"}, []string{"Gamma"}},
		{"query on desc", Filter{Query: "vision"}, []string{"Beta"}},
		{"query on source, case-insensitive", Filter{Query: "arxiv:1706"}, []string{"Alpha"}},
		{"query on DOI", Filter{Query: "10.1/"}, []string{"Gamma"}},
		{"query AND project", Filter{Query: "a", Projects: []string{"p2"}}, []string{"Beta", "Gamma"}},
	}
	for _, tt := range tests {
		got, total, err := s.ListEntries(context.Background(), tt.f, 0, 0)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if !slices.Equal(titlesOf(got), tt.want) || total != len(tt.want) {
			t.Errorf("%s: got %q (total %d), want %q", tt.name, titlesOf(got), total, tt.want)
		}
	}
}

func TestListEntriesPaging(t *testing.T) {
	s := seedStore(t)
	ctx := context.Background()
	page, total, err := s.ListEntries(ctx, Filter{}, 2, 0)
	if err != nil || total != 3 || !slices.Equal(titlesOf(page), []string{"Alpha", "Beta"}) {
		t.Fatalf("page 1 = %q, total %d, %v", titlesOf(page), total, err)
	}
	page, total, err = s.ListEntries(ctx, Filter{}, 2, 2)
	if err != nil || total != 3 || !slices.Equal(titlesOf(page), []string{"Gamma"}) {
		t.Fatalf("page 2 = %q, total %d, %v", titlesOf(page), total, err)
	}
}

func TestLoadLinks(t *testing.T) {
	s := seedStore(t)
	ctx := context.Background()
	all, _, err := s.ListEntries(ctx, Filter{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{all[0].ID, all[2].ID} // Alpha, Gamma
	tags, projects, err := s.LoadLinks(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tags[all[0].ID], []string{"ml", "nlp"}) || tags[all[2].ID] != nil {
		t.Errorf("tags = %v", tags)
	}
	if !slices.Equal(projects[all[2].ID], []string{"p1", "p2"}) || len(projects) != 2 {
		t.Errorf("projects = %v", projects)
	}
}

func TestChangeLinks(t *testing.T) {
	s := seedStore(t)
	ctx := context.Background()
	e, found, err := FindEntry(ctx, s.DB, "alpha")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	err = s.InTx(ctx, func(tx *sql.Tx) error {
		return ChangeLinks(ctx, tx, e.ID, []string{"ml", "new"}, []string{"nlp", "missing"}, []string{"p3"}, []string{"p1"})
	})
	if err != nil {
		t.Fatal(err)
	}
	tags, projects, _ := s.LoadLinks(ctx, []int64{e.ID})
	if !slices.Equal(tags[e.ID], []string{"ml", "new"}) || !slices.Equal(projects[e.ID], []string{"p3"}) {
		t.Errorf("tags = %q, projects = %q", tags[e.ID], projects[e.ID])
	}
}

func TestCountValues(t *testing.T) {
	s := seedStore(t)
	got, err := s.CountValues(context.Background(), "entry_projects", "project")
	if err != nil {
		t.Fatal(err)
	}
	if want := []NameCount{{"p1", 2}, {"p2", 2}}; !slices.Equal(got, want) {
		t.Errorf("CountValues = %v, want %v", got, want)
	}
}

func TestDeleteCascades(t *testing.T) {
	s := seedStore(t)
	ctx := context.Background()
	e, _, _ := FindEntry(ctx, s.DB, "alpha")
	if err := s.InTx(ctx, func(tx *sql.Tx) error { return DeleteEntry(ctx, tx, e.ID) }); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB.QueryRow("SELECT count(*) FROM entry_tags WHERE entry_id = ?", e.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("tags left after delete: %d, %v (foreign_keys pragma not applied?)", n, err)
	}
}
