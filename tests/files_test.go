package tests

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	knowledge "knowledge-mcp/src"
)

func TestSliceLines(t *testing.T) {
	lines := []string{"one", "two", "three", "four", "five"}
	tests := []struct {
		name           string
		start, end, mx int
		text           string
		last           int
		truncated      bool
	}{
		{"middle range", 2, 3, 1000, "two\nthree", 3, false},
		{"end clamped", 4, 99, 1000, "four\nfive", 5, false},
		{"single line", 1, 1, 1000, "one", 1, false},
		// "one\n" + "two\n" is 8 chars; "three\n" would make 14.
		{"stops before overflow", 1, 5, 10, "one\ntwo", 2, false},
		{"exact fit", 1, 2, 8, "one\ntwo", 2, false},
		{"first line cut", 3, 5, 3, "thr", 3, true},
		{"first line exactly fits", 3, 5, 5, "three", 3, false},
		{"start past end", 6, 9, 1000, "", 5, false},
	}
	for _, tt := range tests {
		text, last, truncated := knowledge.SliceLines(lines, tt.start, tt.end, tt.mx)
		if text != tt.text || last != tt.last || truncated != tt.truncated {
			t.Errorf("%s: SliceLines(%d, %d, %d) = %q, %d, %v; want %q, %d, %v",
				tt.name, tt.start, tt.end, tt.mx, text, last, truncated, tt.text, tt.last, tt.truncated)
		}
	}
}

func TestSliceLinesCountsRunes(t *testing.T) {
	lines := []string{strings.Repeat("é", 6), "x"}
	// 6 code points + newline = 7, though the line is 12 bytes.
	text, last, truncated := knowledge.SliceLines(lines, 1, 2, 7)
	if text != lines[0] || last != 1 || truncated {
		t.Errorf("got %q, %d, %v", text, last, truncated)
	}
	text, _, truncated = knowledge.SliceLines(lines, 1, 2, 4)
	if text != "éééé" || !truncated {
		t.Errorf("cut got %q, %v", text, truncated)
	}
}

func TestLoadLines(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := map[string][]string{
		"a\nb\n":   {"a", "b"},
		"a\nb":     {"a", "b"},
		"a\n\nb\n": {"a", "", "b"},
		"\n":       {""},
		"":         {},
	}
	i := 0
	for body, want := range tests {
		i++
		got, err := knowledge.LoadLines(write(string(rune('a'+i)), body))
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("LoadLines(%q) = %q, %v; want %q", body, got, err, want)
		}
	}
	if _, err := knowledge.LoadLines(filepath.Join(dir, "missing")); err == nil {
		t.Error("LoadLines on a missing file succeeded")
	}
}

func TestCopyAndRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.pdf")
	dst := filepath.Join(dir, "dst.pdf")
	if err := os.WriteFile(src, []byte("%PDF-1.7 data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := knowledge.CopyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "%PDF-1.7 data" {
		t.Errorf("copy = %q", b)
	}
	knowledge.RemoveFiles([]string{dst, filepath.Join(dir, "never-existed")})
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("dst still exists: %v", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source was touched: %v", err)
	}
}

func TestReadSourceRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := knowledge.ReadSource(dir); err == nil {
		t.Error("ReadSource on a directory succeeded")
	}
	if _, err := knowledge.ReadSource(filepath.Join(dir, "nope.md")); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("ReadSource on a missing file: %v", err)
	}
}

func TestEntryPaths(t *testing.T) {
	s := &knowledge.Store{FilesDir: "/kb/files"}
	ext := "pdf"
	lines := 3
	e := knowledge.Entry{FileKey: "abc"}
	if got := s.EntryPaths(e); !slices.Equal(got, []string{"/kb/files/abc.content"}) {
		t.Errorf("content only: %q", got)
	}
	e.SummaryLines, e.OriginalExt = &lines, &ext
	want := []string{"/kb/files/abc.content", "/kb/files/abc.summary", "/kb/files/abc.pdf"}
	if got := s.EntryPaths(e); !slices.Equal(got, want) {
		t.Errorf("all files: %q", got)
	}
}
