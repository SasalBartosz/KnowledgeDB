package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// lineWidth caps the characters of each line in search results.
const lineWidth = 500

// newMatcher compiles the search pattern. A literal pattern is quoted, so both modes share one
// code path; case-insensitive matching adds (?i). An empty mode means literal.
func newMatcher(pattern, mode string, caseSensitive bool) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, errors.New("pattern is empty")
	}
	expr := pattern
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "literal":
		expr = regexp.QuoteMeta(pattern)
	case "regex":
	default:
		return nil, fmt.Errorf(`mode must be "literal" or "regex", not %q`, mode)
	}
	if !caseSensitive {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid regex (Go RE2 syntax): %w", err)
	}
	return re, nil
}

// Match is one matching line. The search_lines handler fills Title and Part.
type Match struct {
	Title      string   `json:"title"`
	Part       string   `json:"part"`
	LineNumber int      `json:"line_number"`
	LineText   string   `json:"line_text"`
	Before     []string `json:"before,omitempty"`
	After      []string `json:"after,omitempty"`
}

// chars is the number of code points of line text the match adds to a response.
func (m Match) chars() int {
	n := utf8.RuneCountInString(m.LineText)
	for _, l := range m.Before {
		n += utf8.RuneCountInString(l)
	}
	for _, l := range m.After {
		n += utf8.RuneCountInString(l)
	}
	return n
}

// searchLines matches one file's lines and returns at most maxHits matches, each with up to
// contextN lines before and after. maxHits is what is left of the entry's max_per_entry budget,
// which counts the summary and content together.
func searchLines(lines []string, re *regexp.Regexp, contextN, maxHits int) []Match {
	var out []Match
	for i, line := range lines {
		if len(out) >= maxHits {
			break
		}
		loc := re.FindStringIndex(line)
		if loc == nil {
			continue
		}
		m := Match{LineNumber: i + 1, LineText: trimAround(line, loc[0], loc[1], lineWidth)}
		for j := max(0, i-contextN); j < i; j++ {
			m.Before = append(m.Before, trimHead(lines[j], lineWidth))
		}
		for j := i + 1; j <= min(len(lines)-1, i+contextN); j++ {
			m.After = append(m.After, trimHead(lines[j], lineWidth))
		}
		out = append(out, m)
	}
	return out
}

// trimAround returns line unchanged if it has at most width code points; otherwise a window of
// width code points around the match at byte offsets from..to, centered on it where possible.
func trimAround(line string, from, to, width int) string {
	n := utf8.RuneCountInString(line)
	if n <= width {
		return line
	}
	rf := utf8.RuneCountInString(line[:from])
	rl := utf8.RuneCountInString(line[from:to])
	start := rf
	if rl < width {
		start = rf - (width-rl)/2
	}
	start = max(0, min(start, n-width))
	return string([]rune(line)[start : start+width])
}
