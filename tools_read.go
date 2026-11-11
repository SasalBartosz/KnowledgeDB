package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const descListTags = `List every tag in use, with the number of entries that carry it, ordered by name.
Call this before add_entry and reuse existing tags instead of creating near-duplicates.`

func registerReadTools(srv *mcp.Server, s *Store) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(srv, &mcp.Tool{Name: "list_tags", Description: descListTags, Annotations: ro}, s.handleListTags)
}

// list_tags

type ListTagsIn struct{}

type ListTagsOut struct {
	Tags []NameCount `json:"tags"`
}

func (s *Store) handleListTags(ctx context.Context, _ *mcp.CallToolRequest, _ ListTagsIn) (*mcp.CallToolResult, ListTagsOut, error) {
	tags, err := s.countValues(ctx, "entry_tags", "tag")
	if err != nil {
		return nil, ListTagsOut{}, err
	}
	return nil, ListTagsOut{Tags: tags}, nil
}
