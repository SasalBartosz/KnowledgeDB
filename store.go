package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Store holds the database handle and the directory of entry files.
// Every tool handler is a method on it.
type Store struct {
	DB       *sql.DB
	FilesDir string
}

const schema = `
CREATE TABLE IF NOT EXISTS entries (
  id            INTEGER PRIMARY KEY,
  title         TEXT    NOT NULL,
  title_key     TEXT    NOT NULL UNIQUE,
  short_desc    TEXT    NOT NULL,
  file_key      TEXT    NOT NULL UNIQUE,
  content_lines INTEGER NOT NULL,
  content_chars INTEGER NOT NULL,
  summary_lines INTEGER,
  summary_chars INTEGER,
  original_ext  TEXT,
  source        TEXT,
  created_at    TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS entry_tags (
  entry_id INTEGER NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
  tag      TEXT    NOT NULL,
  PRIMARY KEY (entry_id, tag)
);
CREATE INDEX IF NOT EXISTS entry_tags_by_tag ON entry_tags(tag, entry_id);

CREATE TABLE IF NOT EXISTS entry_projects (
  entry_id INTEGER NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
  project  TEXT    NOT NULL,
  PRIMARY KEY (entry_id, project)
);
CREATE INDEX IF NOT EXISTS entry_projects_by_project ON entry_projects(project, entry_id);
`

// OpenStore creates dir and dir/files if needed, opens the database, and creates the schema.
func OpenStore(dir string) (*Store, error) {
	filesDir := filepath.Join(dir, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filesDir, err)
	}

	// Pragmas live in the DSN so that every pooled connection gets them.
	dsn := "file:" + filepath.Join(dir, "knowledge.sqlite") +
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	return &Store{DB: db, FilesDir: filesDir}, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

// InTx runs fn in a transaction and commits it, or rolls back if fn fails.
// Because of _txlock=immediate, BEGIN takes the write lock, so use this only for writes;
// reads go straight to s.DB.
func (s *Store) InTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
