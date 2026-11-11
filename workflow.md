# Knowledge MCP — Workflow

How to use the knowledge store day to day, for both the user and the agent. `instructions.md` is the short
text the agent sees automatically; this file is the longer walkthrough.

## Setup
1. Build the server: `go build -o knowledge-mcp .` (binary name is up to you).
2. Register it once for all projects in Claude Code:
   `claude mcp add --scope user knowledge -- /absolute/path/to/knowledge-mcp`
3. Data lives in `~/.knowledge-db/` (`knowledge.sqlite` plus `files/`). Set `KNOWLEDGE_DB_DIR` to move it.

Every agent session starts its own server process; they all share the same directory.

## Add a paper
You say something like: *"Add ~/Downloads/paper.pdf to the knowledge base for this project."*

The agent then:
1. Extracts the text to Markdown, one paragraph or list item per line, and saves it to a temporary file.
2. Writes the summary (template below) to a second temporary file, and a short description of at most
   500 characters.
3. Checks it isn't stored already: `list_entries(query="<title or arXiv ID>")`.
4. Calls `list_tags` and `list_projects` and picks existing values where they fit.
5. Stores it. The server copies the three files into its own directory and names them; the agent never
   writes into `~/.knowledge-db/` itself:
   ```
   add_entry(
     title="Attention Is All You Need",
     short_desc="Introduces the Transformer, a sequence model built only on attention.",
     content_path="/tmp/attention.content.md",
     summary_path="/tmp/attention.summary.md",
     file_path="~/Downloads/paper.pdf",
     source="arXiv:1706.03762",
     tags=["transformers", "attention"],
     projects=["KnowledgeDB"]
   )
   ```
6. Deletes the temporary files. The store keeps its own copies, including the original PDF.

Summary template, one bullet per line:
```
# <Title>
- Authors: ...
- Code: <URL or none>
- Source: <arXiv ID, DOI, or URL>

## Methods
- ...

## Benchmarks
- ...

## Results
- ...

## Limitations
- ...
```

Text rules: UTF-8, no hard-wrapped lines, no local file paths, ASCII `-` for minus signs in numbers.

## Add a note
Same tool, less ceremony: write the note to a file, pass its path, and skip the summary and the original
document.
```
add_entry(title="SQLite WAL gotchas", short_desc="Things learned about WAL mode.",
          content_path="/tmp/wal-notes.md", tags=["sqlite"], projects=["KnowledgeDB"])
```

## Find something
Go from cheap to expensive:

| Step | Call | Gives you |
|---|---|---|
| 1. Discover | `list_entries(tags=["attention"], fields=["short_desc"])` | Titles and one-line descriptions |
| 2. Judge | `read_entry(title=..., part="summary")` | The structured summary |
| 3. Locate | `search_lines(pattern="warmup")` | Matching paragraphs with line numbers |
| 4. Read | `read_entry(title=..., part="content", start_line=120, end_line=140)` | The passage around a hit |

Useful variations:
- Only this project's papers: add `projects=["KnowledgeDB"]` to `list_entries` or `search_lines`.
- Inside one paper: `search_lines(pattern=..., titles=["Attention Is All You Need"])`.
- Several phrasings at once, with a regex instead of the default literal match:
  `search_lines(pattern="warm-?up|learning rate schedule", mode="regex")`.
- Surrounding paragraphs: `context=1` (up to 3).
- The original document: `list_entries(titles=[...], fields=["files"])` returns absolute paths.

Search is a plain text scan, not semantic. If a term finds nothing, try synonyms or a regex.

## Organize
- Link an existing entry to another project or tag:
  `update_links(title=..., add_projects=["OtherProject"], add_tags=["optimizers"])`.
- Remove links with `remove_tags` / `remove_projects`.
- See what is in use: `list_tags`, `list_projects` (each with counts).
- Renaming a tag means `update_links` on each entry that has it.

## Fix an entry
Only tags and projects change through the tools. Everything else is manual, on purpose:

| What | How |
|---|---|
| Typo in content or summary | Edit the file under `~/.knowledge-db/files/` (get its path with `fields=["files"]`). Stored line and char counts may go stale; reads still work. |
| Short description or source | `sqlite3 ~/.knowledge-db/knowledge.sqlite "UPDATE entries SET short_desc='...' WHERE title='...'"` |
| Title, or replacing the whole text | `delete_entry`, then `add_entry` again. Copy the entry's files out first, because delete removes them. |

## Delete
`delete_entry(title=...)` removes the entry, its tags and projects, and all its files, including the original
document. There is no undo.

## Back up
Copy the whole `~/.knowledge-db/` directory while no agent is writing. Include the `-wal` and `-shm` files next
to the database.

## Limits at a glance
| Thing | Default | Maximum |
|---|---|---|
| `list_entries` results | 50 | 200 |
| `read_entry` lines per call | 200 | stops at 40,000 chars |
| `search_lines` matches | 50 | 200, stops at 40,000 chars |
| `search_lines` matches per entry | 10 | — |
| `search_lines` context lines | 0 | 3 |
| Line text in search results | — | 500 chars |
| Short description | — | 500 chars |

When a result is cut short, continue from `end_line + 1` (`read_entry`) or `next_offset` (`list_entries`,
`search_lines`).
