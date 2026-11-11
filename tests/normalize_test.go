package tests

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	knowledge "github.com/SasalBartosz/KnowledgeDB/src"
)

func TestNormKey(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Attention Is All You Need", "attention is all you need"},
		{"  spaced \t out\n title  ", "spaced out title"},
		{"Straße", "strasse"},         // full case folding
		{"ＬｏＲＡ", "lora"},              // NFKC folds full-width letters
		{"ﬁne-tuning", "fine-tuning"}, // NFKC expands the ligature
		{"   ", ""},
	}
	for _, tt := range tests {
		if got := knowledge.NormKey(tt.in); got != tt.want {
			t.Errorf("NormKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormKeys(t *testing.T) {
	got := knowledge.NormKeys([]string{"Transformers", " transformers ", "", "Attention", "  "})
	want := []string{"transformers", "attention"}
	if !slices.Equal(got, want) {
		t.Errorf("NormKeys = %q, want %q", got, want)
	}
	if got := knowledge.NormKeys(nil); got == nil || len(got) != 0 {
		t.Errorf("NormKeys(nil) = %#v, want empty non-nil slice", got)
	}
}

func TestFileKey(t *testing.T) {
	k := knowledge.FileKey("attention is all you need")
	if len(k) != 64 || strings.Trim(k, "0123456789abcdef") != "" {
		t.Errorf("FileKey = %q, want 64 lower-case hex chars", k)
	}
	if k == knowledge.FileKey("other") {
		t.Error("different titles produced the same file key")
	}
}

func TestCleanExt(t *testing.T) {
	good := map[string]string{"paper.pdf": "pdf", "Paper.PDF": "pdf", "a.b.epub": "epub", "x.mp4": "mp4"}
	for in, want := range good {
		got, err := knowledge.CleanExt(in)
		if err != nil || got != want {
			t.Errorf("CleanExt(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"README", "file.", "a.tar-gz", "x.pdf~", "notes.content", "notes.Summary", "a.pdf "} {
		if got, err := knowledge.CleanExt(in); err == nil {
			t.Errorf("CleanExt(%q) = %q, want error", in, got)
		}
	}
}

func TestCleanText(t *testing.T) {
	tests := []struct {
		in           string
		want         string
		lines, chars int
	}{
		{"one\ntwo", "one\ntwo\n", 2, 8},
		{"one\r\ntwo\r\n", "one\ntwo\n", 2, 8},
		{"one\rtwo\r", "one\ntwo\n", 2, 8},
		{"one\n\n\n", "one\n", 1, 4},
		{"\xef\xbb\xbfbom", "bom\n", 1, 4},
		{"ünïcödé\n", "ünïcödé\n", 1, 8},
		{"a\n\nb\n", "a\n\nb\n", 3, 5}, // blank lines inside are kept
	}
	for _, tt := range tests {
		got, lines, chars, err := knowledge.CleanText([]byte(tt.in))
		if err != nil || got != tt.want || lines != tt.lines || chars != tt.chars {
			t.Errorf("CleanText(%q) = %q, %d, %d, %v; want %q, %d, %d",
				tt.in, got, lines, chars, err, tt.want, tt.lines, tt.chars)
		}
	}
	for _, in := range []string{"", "\n\n", "  \n \r\n", "bad \xff utf8"} {
		if _, _, _, err := knowledge.CleanText([]byte(in)); err == nil {
			t.Errorf("CleanText(%q) succeeded, want error", in)
		}
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, err := knowledge.ExpandPath("~/papers/a.pdf"); err != nil || got != filepath.Join(home, "papers/a.pdf") {
		t.Errorf("ExpandPath(~/...) = %q, %v", got, err)
	}
	if got, err := knowledge.ExpandPath("/tmp/../tmp/a.md"); err != nil || got != "/tmp/a.md" {
		t.Errorf("ExpandPath(abs) = %q, %v", got, err)
	}
	for _, in := range []string{"relative/a.md", "a.md", "~user/a.md", "~", ""} {
		if _, err := knowledge.ExpandPath(in); err == nil {
			t.Errorf("ExpandPath(%q) succeeded, want error", in)
		}
	}
}

func TestTrimHead(t *testing.T) {
	if got := knowledge.TrimHead("héllo", 2); got != "hé" {
		t.Errorf("TrimHead = %q, want %q", got, "hé")
	}
	if got := knowledge.TrimHead("abc", 3); got != "abc" {
		t.Errorf("TrimHead at exact width = %q", got)
	}
	if got := knowledge.TrimHead("abc", 10); got != "abc" {
		t.Errorf("TrimHead short = %q", got)
	}
}
