package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// testEnv is a store in a temp directory, served by newServer and called through a real
// MCP client session, so input and output schemas are exercised too.
type testEnv struct {
	t       *testing.T
	store   *Store
	session *mcp.ClientSession
	srcDir  string // where test "caller" files live
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()
	if _, err := newServer(s).Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })

	src := filepath.Join(dir, "src")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	return &testEnv{t: t, store: s, session: cs, srcDir: src}
}

// call invokes a tool and decodes its structured output into out. A tool error comes back as an error.
func (env *testEnv) call(name string, args map[string]any, out any) error {
	env.t.Helper()
	res, err := env.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		env.t.Fatalf("%s: protocol error: %v", name, err)
	}
	if res.IsError {
		var msg []string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				msg = append(msg, tc.Text)
			}
		}
		return errors.New(strings.Join(msg, "\n"))
	}
	if out != nil {
		reflect.ValueOf(out).Elem().SetZero() // Unmarshal would keep fields absent from this response
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			env.t.Fatal(err)
		}
		if err := json.Unmarshal(b, out); err != nil {
			env.t.Fatalf("%s: decoding %s: %v", name, b, err)
		}
	}
	return nil
}

func (env *testEnv) mustCall(name string, args map[string]any, out any) {
	env.t.Helper()
	if err := env.call(name, args, out); err != nil {
		env.t.Fatalf("%s(%v): %v", name, args, err)
	}
}

// wantErr calls a tool that must fail, with an error containing want.
func (env *testEnv) wantErr(name string, args map[string]any, want string) {
	env.t.Helper()
	err := env.call(name, args, nil)
	if err == nil {
		env.t.Fatalf("%s(%v) succeeded, want error containing %q", name, args, want)
	}
	if !strings.Contains(err.Error(), want) {
		env.t.Fatalf("%s(%v) error = %q, want it to contain %q", name, args, err, want)
	}
}

// source writes a caller file and returns its absolute path.
func (env *testEnv) source(name, body string) string {
	env.t.Helper()
	p := filepath.Join(env.srcDir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		env.t.Fatal(err)
	}
	return p
}

// storedFiles lists the names in the store's files directory.
func (env *testEnv) storedFiles() []string {
	env.t.Helper()
	des, err := os.ReadDir(env.store.filesDir)
	if err != nil {
		env.t.Fatal(err)
	}
	var names []string
	for _, de := range des {
		names = append(names, de.Name())
	}
	return names
}

// addNote adds an entry with only content, tags, and projects.
func (env *testEnv) addNote(title, content string, tags, projects []string) {
	env.t.Helper()
	env.mustCall("add_entry", map[string]any{
		"title": title, "short_desc": "About " + title,
		"content_path": env.source(fileKey(title)+".md", content),
		"tags":         tags, "projects": projects,
	}, nil)
}

func TestAddAndDelete(t *testing.T) {
	env := newTestEnv(t)
	content := env.source("paper.md", "Abstract line.\r\nSecond paragraph.\r\n\r\n")
	summary := env.source("paper.summary.md", "# Attention\n- Authors: Vaswani et al.\n")
	pdf := env.source("paper.PDF", "%PDF-1.7")

	var added AddEntryOut
	env.mustCall("add_entry", map[string]any{
		"title":        "  Attention Is   All You Need ",
		"short_desc":   "Introduces the Transformer.",
		"content_path": content,
		"summary_path": summary,
		"file_path":    pdf,
		"source":       "arXiv:1706.03762",
		"tags":         []string{"Transformers", "transformers ", "Attention"},
		"projects":     []string{"KnowledgeDB"},
	}, &added)

	if added.Title != "Attention Is All You Need" {
		t.Errorf("title = %q", added.Title)
	}
	if added.Source == nil || *added.Source != "arXiv:1706.03762" {
		t.Errorf("source = %v", added.Source)
	}
	if !slices.Equal(added.Tags, []string{"transformers", "attention"}) || !slices.Equal(added.Projects, []string{"knowledgedb"}) {
		t.Errorf("tags = %q, projects = %q", added.Tags, added.Projects)
	}
	if added.Stats.ContentLines != 2 || added.Stats.SummaryLines == nil || *added.Stats.SummaryLines != 2 {
		t.Errorf("stats = %+v", added.Stats)
	}
	if !strings.HasSuffix(added.Files.Original, ".pdf") || added.Files.Summary == "" {
		t.Errorf("files = %+v", added.Files)
	}
	if b, _ := os.ReadFile(added.Files.Content); string(b) != "Abstract line.\nSecond paragraph.\n" {
		t.Errorf("stored content = %q", b)
	}
	if b, _ := os.ReadFile(added.Files.Original); string(b) != "%PDF-1.7" {
		t.Errorf("stored original = %q", b)
	}
	for _, p := range []string{content, summary, pdf} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("caller file %s was touched: %v", p, err)
		}
	}

	env.wantErr("add_entry", map[string]any{
		"title": "attention is all you need", "short_desc": "dup", "content_path": content,
	}, "already exists")

	var tags ListTagsOut
	env.mustCall("list_tags", nil, &tags)
	if want := []NameCount{{"attention", 1}, {"transformers", 1}}; !slices.Equal(tags.Tags, want) {
		t.Errorf("list_tags = %v, want %v", tags.Tags, want)
	}

	var deleted DeleteEntryOut
	env.mustCall("delete_entry", map[string]any{"title": "ATTENTION is all you need"}, &deleted)
	if deleted.Title != "Attention Is All You Need" {
		t.Errorf("deleted title = %q", deleted.Title)
	}
	if left := env.storedFiles(); len(left) != 0 {
		t.Errorf("files left after delete: %q", left)
	}
	env.mustCall("list_tags", nil, &tags)
	if len(tags.Tags) != 0 {
		t.Errorf("tags left after delete: %v", tags.Tags)
	}
	env.wantErr("delete_entry", map[string]any{"title": "Attention Is All You Need"}, "no entry titled")
}

func TestAddEntryValidation(t *testing.T) {
	env := newTestEnv(t)
	good := env.source("good.md", "text\n")
	base := func(over map[string]any) map[string]any {
		args := map[string]any{"title": "T", "short_desc": "d", "content_path": good}
		for k, v := range over {
			args[k] = v
		}
		return args
	}

	env.wantErr("add_entry", base(map[string]any{"title": "  "}), "title is empty")
	env.wantErr("add_entry", base(map[string]any{"short_desc": strings.Repeat("é", 501)}), "short_desc is 501 characters")
	env.wantErr("add_entry", base(map[string]any{"content_path": "relative.md"}), "must be absolute or start with ~/")
	env.wantErr("add_entry", base(map[string]any{"content_path": filepath.Join(env.srcDir, "nope.md")}), "does not exist")
	env.wantErr("add_entry", base(map[string]any{"content_path": env.source("bad.md", "bad \xff")}), "not valid UTF-8")
	env.wantErr("add_entry", base(map[string]any{"content_path": env.source("empty.md", "\n\n")}), "text is empty")
	env.wantErr("add_entry", base(map[string]any{"summary_path": env.srcDir}), "not a regular file")
	env.wantErr("add_entry", base(map[string]any{"file_path": env.source("noext", "x")}), "has no extension")
	env.wantErr("add_entry", base(map[string]any{"file_path": env.source("x.content", "x")}), "reserved")
	env.wantErr("add_entry", map[string]any{"title": "T", "short_desc": "d"}, "content_path")

	if left := env.storedFiles(); len(left) != 0 {
		t.Errorf("files left after rejected adds: %q", left)
	}

	// A 500-character description is accepted.
	env.mustCall("add_entry", base(map[string]any{"short_desc": strings.Repeat("é", 500)}), nil)
}

func TestFailedAddLeavesNoFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read unreadable files")
	}
	env := newTestEnv(t)
	pdf := env.source("locked.pdf", "%PDF")
	if err := os.Chmod(pdf, 0); err != nil {
		t.Fatal(err)
	}
	// The content and summary are written before copying the original fails.
	env.wantErr("add_entry", map[string]any{
		"title": "Locked", "short_desc": "d",
		"content_path": env.source("c.md", "c\n"), "summary_path": env.source("s.md", "s\n"),
		"file_path": pdf,
	}, "storing original document")

	if left := env.storedFiles(); len(left) != 0 {
		t.Errorf("files left after failed add: %q", left)
	}
	var tags ListTagsOut
	env.mustCall("list_tags", nil, &tags)
	env.wantErr("delete_entry", map[string]any{"title": "Locked"}, "no entry titled")
}

func TestNotFoundSuggestsTitles(t *testing.T) {
	env := newTestEnv(t)
	env.addNote("Attention Is All You Need", "a\n", nil, nil)
	env.addNote("Sparse Attention Tricks", "b\n", nil, nil)
	env.addNote("LoRA", "c\n", nil, nil)

	env.wantErr("delete_entry", map[string]any{"title": "attention"},
		`did you mean: "Attention Is All You Need", "Sparse Attention Tricks"`)
	env.wantErr("delete_entry", map[string]any{"title": "Mamba"}, "use list_entries")
}

func TestListEntriesAndLinks(t *testing.T) {
	env := newTestEnv(t)
	env.addNote("Gamma", "g\n", nil, []string{"P1"})
	env.addNote("alpha", "a\n", []string{"ml"}, []string{"P1"})
	env.addNote("Beta", "b\n", []string{"ml", "vision"}, []string{"P2"})

	// Titles only by default: no other keys in the raw output.
	var raw map[string]any
	env.mustCall("list_entries", nil, &raw)
	entries := raw["entries"].([]any)
	if len(entries) != 3 || raw["total"].(float64) != 3 || raw["next_offset"] != nil {
		t.Fatalf("list_entries = %v", raw)
	}
	for i, want := range []string{"alpha", "Beta", "Gamma"} {
		e := entries[i].(map[string]any)
		if len(e) != 1 || e["title"] != want {
			t.Errorf("entry %d = %v, want only title %q", i, e, want)
		}
	}

	var out ListEntriesOut
	env.mustCall("list_entries", map[string]any{"fields": []string{"short_desc", "tags", "files", "stats"}, "projects": []string{"p1"}}, &out)
	if len(out.Entries) != 2 {
		t.Fatalf("p1 entries = %+v", out.Entries)
	}
	gamma := out.Entries[1]
	if gamma.Title != "Gamma" || gamma.ShortDesc == nil || gamma.Tags == nil || len(gamma.Tags) != 0 ||
		gamma.Files == nil || gamma.Files.Content == "" || gamma.Stats == nil || gamma.Source != nil || gamma.Projects != nil {
		t.Errorf("gamma = %+v", gamma)
	}

	env.mustCall("list_entries", map[string]any{"limit": 2}, &out)
	if len(out.Entries) != 2 || out.NextOffset == nil || *out.NextOffset != 2 || out.Total != 3 {
		t.Errorf("page 1 = %+v", out)
	}
	env.mustCall("list_entries", map[string]any{"limit": 2, "offset": 2}, &out)
	if len(out.Entries) != 1 || out.NextOffset != nil {
		t.Errorf("page 2 = %+v", out)
	}
	env.mustCall("list_entries", map[string]any{"tags": []string{"ML"}, "query": "BET"}, &out)
	if len(out.Entries) != 1 || out.Entries[0].Title != "Beta" {
		t.Errorf("tag+query = %+v", out)
	}
	env.wantErr("list_entries", map[string]any{"fields": []string{"summary"}}, `unknown field "summary"`)

	var links UpdateLinksOut
	env.mustCall("update_links", map[string]any{
		"title": "BETA", "add_tags": []string{"New", "ml"}, "remove_tags": []string{"vision", "absent"},
		"add_projects": []string{"p3"}, "remove_projects": []string{"p2"},
	}, &links)
	if links.Title != "Beta" || !slices.Equal(links.Tags, []string{"ml", "new"}) || !slices.Equal(links.Projects, []string{"p3"}) {
		t.Errorf("update_links = %+v", links)
	}
	env.mustCall("update_links", map[string]any{"title": "Beta", "remove_tags": []string{"ml", "new"}, "remove_projects": []string{"p3"}}, &links)
	if links.Tags == nil || len(links.Tags) != 0 || len(links.Projects) != 0 {
		t.Errorf("emptied links = %+v", links)
	}
	env.wantErr("update_links", map[string]any{"title": "Delta", "add_tags": []string{"x"}}, "no entry titled")

	var projects ListProjectsOut
	env.mustCall("list_projects", nil, &projects)
	if want := []NameCount{{"p1", 2}}; !slices.Equal(projects.Projects, want) {
		t.Errorf("list_projects = %v, want %v", projects.Projects, want)
	}
}

// numbered returns n lines "line 1" .. "line n".
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	return b.String()
}

func TestReadEntry(t *testing.T) {
	env := newTestEnv(t)
	env.mustCall("add_entry", map[string]any{
		"title": "Paper", "short_desc": "d",
		"content_path": env.source("c.md", numbered(450)),
		"summary_path": env.source("s.md", "# Paper\n- Authors: A\n"),
	}, nil)
	env.addNote("Note", "only content\n", nil, nil)

	var out ReadEntryOut
	env.mustCall("read_entry", map[string]any{"title": "paper", "part": "content"}, &out)
	if out.Title != "Paper" || out.StartLine != 1 || out.EndLine != 200 || out.TotalLines != 450 || out.Truncated {
		t.Errorf("default range = %+v", out)
	}
	if !strings.HasPrefix(out.Text, "line 1\n") || !strings.HasSuffix(out.Text, "\nline 200") {
		t.Errorf("default text = %q...", trimHead(out.Text, 40))
	}

	env.mustCall("read_entry", map[string]any{"title": "Paper", "part": "content", "start_line": 401}, &out)
	if out.StartLine != 401 || out.EndLine != 450 {
		t.Errorf("tail range = %d..%d", out.StartLine, out.EndLine)
	}
	env.mustCall("read_entry", map[string]any{"title": "Paper", "part": "content", "start_line": 10, "end_line": 12}, &out)
	if out.Text != "line 10\nline 11\nline 12" || out.EndLine != 12 {
		t.Errorf("explicit range = %+v", out)
	}

	env.mustCall("read_entry", map[string]any{"title": "Paper", "part": "summary"}, &out)
	if out.Text != "# Paper\n- Authors: A" || out.TotalLines != 2 || out.Part != "summary" {
		t.Errorf("summary = %+v", out)
	}

	env.wantErr("read_entry", map[string]any{"title": "Paper", "part": "content", "start_line": 451}, "past the end")
	env.wantErr("read_entry", map[string]any{"title": "Paper", "part": "content", "start_line": 5, "end_line": 4}, "before start_line")
	env.wantErr("read_entry", map[string]any{"title": "Paper", "part": "content", "start_line": -1}, "at least 1")
	env.wantErr("read_entry", map[string]any{"title": "Paper", "part": "abstract"}, `part must be "summary" or "content"`)
	env.wantErr("read_entry", map[string]any{"title": "Note", "part": "summary"}, `has no summary; read part="content"`)
	env.wantErr("read_entry", map[string]any{"title": "Missing", "part": "content"}, "no entry titled")
}

func TestReadEntryCharCap(t *testing.T) {
	env := newTestEnv(t)
	long := strings.Repeat("x", 15000)
	env.addNote("Long lines", long+"\n"+long+"\n"+long+"\n", nil, nil)
	env.addNote("One huge line", strings.Repeat("é", 50000)+"\nnext\n", nil, nil)

	var out ReadEntryOut
	env.mustCall("read_entry", map[string]any{"title": "Long lines", "part": "content"}, &out)
	if out.EndLine != 2 || out.Truncated || len(out.Text) != 2*15000+1 {
		t.Errorf("long lines: end_line %d, truncated %v, %d chars", out.EndLine, out.Truncated, len(out.Text))
	}
	env.mustCall("read_entry", map[string]any{"title": "One huge line", "part": "content"}, &out)
	if out.EndLine != 1 || !out.Truncated || utf8.RuneCountInString(out.Text) != 40000 {
		t.Errorf("huge line: end_line %d, truncated %v, %d chars", out.EndLine, out.Truncated, utf8.RuneCountInString(out.Text))
	}
}

func TestReadEntryUsesFileLineCount(t *testing.T) {
	env := newTestEnv(t)
	env.addNote("Edited", numbered(10), nil, nil)
	var listed ListEntriesOut
	env.mustCall("list_entries", map[string]any{"fields": []string{"files"}}, &listed)
	if err := os.WriteFile(listed.Entries[0].Files.Content, []byte("fixed\ntext\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out ReadEntryOut
	env.mustCall("read_entry", map[string]any{"title": "Edited", "part": "content"}, &out)
	if out.TotalLines != 2 || out.Text != "fixed\ntext" {
		t.Errorf("after hand edit = %+v", out)
	}
}
