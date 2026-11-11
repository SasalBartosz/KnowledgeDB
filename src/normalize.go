package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// NormKey normalizes a title, tag, or project for storage and lookup:
// NFKC, Unicode case fold, trim, and collapse internal whitespace to single spaces.
func NormKey(s string) string {
	s = norm.NFKC.String(s)
	s = cases.Fold().String(s) // a Caser is stateful, so make one per call
	return strings.Join(strings.Fields(s), " ")
}

// NormKeys normalizes each value and drops empties and duplicates, keeping first-seen order.
func NormKeys(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range in {
		k := NormKey(v)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// FileKey is the name stem of an entry's files: the SHA-256 hex of its title key.
func FileKey(titleKey string) string {
	sum := sha256.Sum256([]byte(titleKey))
	return hex.EncodeToString(sum[:])
}

// CleanExt returns the lower-case extension of filename without the dot. It must be
// non-empty, ASCII alphanumeric, and not "content" or "summary", which name the entry's own files.
func CleanExt(filename string) (string, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	if ext == "" {
		return "", fmt.Errorf("file %q has no extension; the original document needs one (e.g. .pdf)", filename)
	}
	for _, r := range ext {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return "", fmt.Errorf("file %q has extension %q; only letters and digits are allowed", filename, ext)
		}
	}
	if ext == "content" || ext == "summary" {
		return "", fmt.Errorf("file %q has extension %q, which is reserved for the entry's own text files", filename, ext)
	}
	return ext, nil
}

// CleanText validates and normalizes stored text: it must be UTF-8 and not empty; a leading
// byte order mark is dropped, CRLF and CR become LF, and the text ends with exactly one newline.
// lines is the number of lines and chars the number of code points, newlines included.
func CleanText(raw []byte) (text string, lines, chars int, err error) {
	if !utf8.Valid(raw) {
		return "", 0, 0, errors.New("text is not valid UTF-8")
	}
	text = strings.TrimPrefix(string(raw), "\xef\xbb\xbf")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return "", 0, 0, errors.New("text is empty")
	}
	text += "\n"
	return text, strings.Count(text, "\n"), utf8.RuneCountInString(text), nil
}

// ExpandPath accepts an absolute path or one starting with ~/ and returns it absolute and clean.
func ExpandPath(p string) (string, error) {
	switch {
	case strings.HasPrefix(p, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expanding %q: %w", p, err)
		}
		return filepath.Join(home, p[2:]), nil
	case filepath.IsAbs(p):
		return filepath.Clean(p), nil
	default:
		return "", fmt.Errorf("path %q must be absolute or start with ~/", p)
	}
}

// TrimHead returns the first width code points of s, cutting on a rune boundary.
func TrimHead(s string, width int) string {
	n := 0
	for i := range s {
		if n == width {
			return s[:i]
		}
		n++
	}
	return s
}
