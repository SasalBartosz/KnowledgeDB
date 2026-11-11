package main

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNewMatcher(t *testing.T) {
	tests := []struct {
		pattern, mode string
		caseSensitive bool
		line          string
		want          bool
	}{
		{"a.b", "", false, "xa.by", true},
		{"a.b", "literal", false, "axb", false}, // literal quotes metacharacters
		{"a.b", "regex", false, "axb", true},
		{"Warmup", "", false, "WARMUP steps", true},
		{"Warmup", "", true, "WARMUP steps", false},
		{"warm-?up|schedule", "regex", false, "Learning rate SCHEDULE", true},
		{"ÜBER", "", false, "über alles", true}, // (?i) folds Unicode
		{"-0.5", "", false, "loss of -0.5", true},
		{"(1)", "literal", false, "see (1)", true},
	}
	for _, tt := range tests {
		re, err := newMatcher(tt.pattern, tt.mode, tt.caseSensitive)
		if err != nil {
			t.Fatalf("newMatcher(%q, %q): %v", tt.pattern, tt.mode, err)
		}
		if got := re.MatchString(tt.line); got != tt.want {
			t.Errorf("newMatcher(%q, %q, %v) on %q = %v, want %v", tt.pattern, tt.mode, tt.caseSensitive, tt.line, got, tt.want)
		}
	}

	for _, bad := range []struct{ pattern, mode, want string }{
		{"", "", "pattern is empty"},
		{"x", "fuzzy", `mode must be "literal" or "regex"`},
		{"(unclosed", "regex", "invalid regex"},
	} {
		if _, err := newMatcher(bad.pattern, bad.mode, false); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("newMatcher(%q, %q) error = %v, want %q", bad.pattern, bad.mode, err, bad.want)
		}
	}
}

func TestSearchLinesContextAtEdges(t *testing.T) {
	lines := []string{"hit one", "a", "b", "c", "hit two"}
	re, _ := newMatcher("hit", "", false)

	got := searchLines(lines, re, 2, 10)
	if len(got) != 2 {
		t.Fatalf("got %d matches", len(got))
	}
	first, last := got[0], got[1]
	if first.LineNumber != 1 || first.Before != nil || !slices.Equal(first.After, []string{"a", "b"}) {
		t.Errorf("first = %+v", first)
	}
	if last.LineNumber != 5 || !slices.Equal(last.Before, []string{"b", "c"}) || last.After != nil {
		t.Errorf("last = %+v", last)
	}

	if got := searchLines(lines, re, 0, 10); got[0].Before != nil || got[0].After != nil {
		t.Errorf("context 0 = %+v", got[0])
	}
}

func TestSearchLinesHitLimit(t *testing.T) {
	lines := []string{"x", "x", "y", "x", "x"}
	re, _ := newMatcher("x", "", false)
	got := searchLines(lines, re, 0, 3)
	var nums []int
	for _, m := range got {
		nums = append(nums, m.LineNumber)
	}
	if !slices.Equal(nums, []int{1, 2, 4}) {
		t.Errorf("line numbers = %v", nums)
	}
	if got := searchLines(lines, re, 0, 0); len(got) != 0 {
		t.Errorf("zero budget returned %d matches", len(got))
	}
}

func TestSearchLinesTrimsLongLines(t *testing.T) {
	long := strings.Repeat("a", 1000) + "NEEDLE" + strings.Repeat("b", 1000)
	ctxLine := strings.Repeat("é", 800)
	re, _ := newMatcher("needle", "", false)
	got := searchLines([]string{ctxLine, long}, re, 1, 10)
	m := got[0]
	if n := utf8.RuneCountInString(m.LineText); n != lineWidth || !strings.Contains(m.LineText, "NEEDLE") {
		t.Errorf("line_text has %d chars, contains needle: %v", n, strings.Contains(m.LineText, "NEEDLE"))
	}
	if m.Before[0] != strings.Repeat("é", lineWidth) {
		t.Errorf("context line not cut to its first %d chars", lineWidth)
	}
}

func TestTrimAround(t *testing.T) {
	line := "0123456789abcdefghij" // 20 chars
	tests := []struct {
		from, to int
		want     string
	}{
		{10, 11, "89abc"}, // centered on "a"
		{0, 1, "01234"},   // window clamped to the start
		{19, 20, "fghij"}, // window clamped to the end
		{5, 15, "56789"},  // match longer than the window starts at the match
		{9, 12, "89abc"},  // three-char match with one char each side
	}
	for _, tt := range tests {
		if got := trimAround(line, tt.from, tt.to, 5); got != tt.want {
			t.Errorf("trimAround(%d, %d) = %q, want %q", tt.from, tt.to, got, tt.want)
		}
	}
	if got := trimAround("short", 0, 1, 10); got != "short" {
		t.Errorf("short line changed: %q", got)
	}

	// Offsets are bytes; the window is counted in code points.
	multi := "ééééé" + "X" + "ééééé"
	from := strings.Index(multi, "X")
	if got := trimAround(multi, from, from+1, 3); got != "éXé" {
		t.Errorf("multi-byte window = %q", got)
	}
}
