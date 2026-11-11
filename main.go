// Command knowledge-mcp is a personal MCP server that stores whitepapers and notes
// in SQLite plus a directory of text files, and serves them over stdio.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName    = "knowledge-mcp"
	serverVersion = "0.1.0"
)

//go:embed instructions.md
var instructionsText string

func main() {
	// stdout carries the MCP protocol; all logging goes to stderr.
	log.SetOutput(os.Stderr)
	log.SetPrefix(serverName + ": ")

	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	dir, err := baseDir()
	if err != nil {
		return err
	}
	s, err := OpenStore(dir)
	if err != nil {
		return err
	}
	defer s.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return newServer(s).Run(ctx, &mcp.StdioTransport{})
}

// baseDir returns KNOWLEDGE_DB_DIR, or ~/.knowledge-db when it is unset, as an absolute path.
func baseDir() (string, error) {
	if dir := os.Getenv("KNOWLEDGE_DB_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, ".knowledge-db"), nil
}

func newServer(s *Store) *mcp.Server {
	srv := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructionsText},
	)
	registerReadTools(srv, s)
	registerWriteTools(srv, s)
	return srv
}
