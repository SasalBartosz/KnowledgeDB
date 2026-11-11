package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	partContent = "content"
	partSummary = "summary"
)

func (s *Store) contentPath(key string) string {
	return filepath.Join(s.filesDir, key+"."+partContent)
}

func (s *Store) summaryPath(key string) string {
	return filepath.Join(s.filesDir, key+"."+partSummary)
}

func (s *Store) originalPath(key, ext string) string {
	return filepath.Join(s.filesDir, key+"."+ext)
}

// partPath is the stored text file for part ("content" or "summary").
func (s *Store) partPath(e Entry, part string) string {
	if part == partSummary {
		return s.summaryPath(e.FileKey)
	}
	return s.contentPath(e.FileKey)
}

// EntryFiles holds the absolute paths of an entry's files; empty means the file does not exist.
type EntryFiles struct {
	Content  string `json:"content"`
	Summary  string `json:"summary,omitempty"`
	Original string `json:"original,omitempty"`
}

// entryFiles returns the paths of the files the row says exist.
func (s *Store) entryFiles(e Entry) EntryFiles {
	f := EntryFiles{Content: s.contentPath(e.FileKey)}
	if e.hasSummary() {
		f.Summary = s.summaryPath(e.FileKey)
	}
	if e.OriginalExt != nil {
		f.Original = s.originalPath(e.FileKey, *e.OriginalExt)
	}
	return f
}

// entryPaths lists the files the row says exist.
func (s *Store) entryPaths(e Entry) []string {
	f := s.entryFiles(e)
	paths := []string{f.Content}
	for _, p := range []string{f.Summary, f.Original} {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// readSource reads one of the caller's files (content or summary source).
func readSource(path string) ([]byte, error) {
	if err := checkRegular(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// checkRegular reports an error unless path is an existing regular file.
func checkRegular(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("file %s does not exist", path)
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

// writeText writes text to dst. No temp file and rename: files are written inside the
// add transaction before the row exists, so no reader can see a partial file.
func writeText(dst, text string) error {
	return os.WriteFile(dst, []byte(text), 0o644)
}

// copyFile copies src to dst, replacing dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// removeFiles deletes paths, best effort. Missing files are ignored; other failures are logged.
func removeFiles(paths []string) {
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Printf("removing %s: %v", p, err)
		}
	}
}

// loadLines reads a whole text file, drops one trailing newline, and splits it into lines.
// An empty file has no lines.
func loadLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return []string{}, nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n"), nil
}

// sliceLines returns lines start..end (1-based, inclusive, end clamped to the last line), joined
// with newlines, stopping before the line that would take the text past maxChars code points
// (each line counts with its newline). If the first line alone is too long it is cut to maxChars
// and truncated is true. last is the number of the last line returned, start-1 if none.
func sliceLines(lines []string, start, end, maxChars int) (text string, last int, truncated bool) {
	end = min(end, len(lines))
	if start < 1 || start > end {
		return "", start - 1, false
	}

	var b strings.Builder
	chars := 0
	last = start - 1
	for n := start; n <= end; n++ {
		line := lines[n-1]
		size := utf8.RuneCountInString(line) + 1
		if chars+size > maxChars {
			if n == start {
				cut := trimHead(line, maxChars)
				return cut, n, len(cut) < len(line)
			}
			break
		}
		if n > start {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		chars += size
		last = n
	}
	return b.String(), last, false
}
