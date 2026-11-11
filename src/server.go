// Package knowledge implements the Knowledge MCP server: a SQLite index plus a directory of text
// files, served as MCP tools. Command knowledge-mcp in the module root runs it over stdio.
package knowledge

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ServerName    = "knowledge-mcp"
	ServerVersion = "0.1.0"
)

// NewServer builds the MCP server with every tool registered as a method on s.
// instructions is the text the client shows the model for the whole session.
func NewServer(s *Store, instructions string) *mcp.Server {
	srv := mcp.NewServer(
		&mcp.Implementation{Name: ServerName, Version: ServerVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	registerReadTools(srv, s)
	registerWriteTools(srv, s)
	return srv
}
