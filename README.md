# Knowledge MCP

A small personal MCP server that stores whitepapers and notes once and makes them available to AI agents in
every project.

## Why

I read papers and keep notes that matter to more than one project. Without a shared store, each new agent
session has to be pointed at the PDF again and re-read it from scratch, which is slow and burns context.

I wanted something simple:

- one Go binary, one SQLite file, one folder of text files;
- no embeddings, vector database, or background service;
- the agent does the smart work (extracting text, writing the summary), the server only stores and serves.

## How it works

- **Add once.** The agent extracts a paper to Markdown, writes a structured summary, and calls `add_entry`
  with the file paths. The server copies them into `~/.knowledge-db/` and indexes the metadata.
- **Read cheaply.** An agent lists entries, reads the short summary first, and only then searches or reads
  the specific paragraphs it needs.
- **Share across projects.** Entries carry tags and project names, so any session can ask for "the papers
  linked to this project" or search across everything.

## Example

In any project:

> "Add ~/Downloads/lora.pdf to the knowledge base for this project."

Weeks later, in another project:

> "What rank did the LoRA paper use in its GPT-3 experiments?"

The agent finds the entry with `list_entries`, checks the summary, runs `search_lines` for "rank", and reads
just the matching paragraphs, without opening the PDF again.

## Tools

| Tool                         | Purpose                                         |
| ---------------------------- | ----------------------------------------------- |
| `list_entries`               | Discover entries by tag, project, or text query |
| `read_entry`                 | Read a summary or a line range of content       |
| `search_lines`               | Literal or regex search across stored text      |
| `add_entry`                  | Store a new entry from files                    |
| `update_links`               | Change an entry's tags and projects             |
| `delete_entry`               | Remove an entry and its files                   |
| `list_tags`, `list_projects` | See which values are in use                     |

## Install

Requires Go (see `go.mod` for the version).

1. Make sure Go's install directory is on your `PATH`. By default it is `~/go/bin`; `go env GOBIN GOPATH`
   shows where it is on your machine. In `~/.zshrc`:

   ```sh
   export PATH="$HOME/go/bin:$PATH"
   ```

2. Install the binary:

   ```sh
   go install github.com/SasalBartosz/KnowledgeDB/cmd/knowledge-mcp@latest
   ```

   `@latest` resolves to the newest release tag, or to the newest commit on `main` if there are no tags. Run
   the same command again to update. Right after a push, `@main` picks up the new commit if `@latest` has not
   caught up yet.

To build from a local checkout instead: `go build -o knowledge-mcp ./cmd/knowledge-mcp`.

## Setup

The agent starts the server itself and talks to it over stdio; there is nothing to run separately. Register
it once per harness. The bare command name works when the harness is started from a shell that has
`~/go/bin` on its `PATH`; otherwise use the absolute path (e.g. `/Users/<you>/go/bin/knowledge-mcp`).

**Claude Code:**

```sh
claude mcp add --scope user knowledge -- knowledge-mcp
```

**Pi:** add the server to `~/.pi/agent/mcp.json` (all projects) or `.pi/mcp.json` (one project). This works
with Pi's built-in MCP support (0.99+) and with `pi-mcp-adapter`:

```json
{
  "mcpServers": {
    "knowledge": { "command": "knowledge-mcp" }
  }
}
```

Check the connection with `/mcp` inside a Pi session.

Data lives in `~/.knowledge-db/`; set `KNOWLEDGE_DB_DIR` to change that.

## Documents

- [workflow.md](workflow.md): day-to-day usage with example calls.
- [instructions.md](cmd/knowledge-mcp/instructions.md): the short guidance the server sends to the agent.
