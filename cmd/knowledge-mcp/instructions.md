Personal knowledge store of whitepapers and notes, shared across projects. Entries are identified by title.

Finding information, cheapest step first:
1. `list_entries` to discover entries. Filter with `tags`, `projects`, or `query`; request `short_desc` in `fields` to see what each entry is about.
2. `read_entry` with `part="summary"` to decide whether an entry is relevant. Some entries (notes) have no summary; go straight to step 3 for those.
3. `search_lines` to locate passages, then `read_entry` with `part="content"` and a line range around the hits. Do not read a whole content from start to end unless asked to.

Text is stored one paragraph per line, so a line number refers to a paragraph. Results are paged: continue `read_entry` from `end_line + 1`, and `list_entries` or `search_lines` from `next_offset`. Titles, tags, and projects match case-insensitively.

Adding entries:
- First call `list_entries` with `query` set to the title or the arXiv ID/DOI, so the same paper is not stored twice.
- Call `list_tags` and `list_projects` and reuse existing values instead of creating near-duplicates.
- A project is the name of the repository directory the work belongs to, e.g. `KnowledgeDB`.
- Follow the content and summary format given in the `add_entry` description.
- Entries are insert-only. Afterwards only tags and projects can change, with `update_links`.
