// Command codebase-analyser-mcp serves the analyser over MCP on stdio.
//
// stdout carries the JSON-RPC stream and nothing else - every diagnostic
// goes to stderr, or the host's parser breaks.
package main

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase-analyser/internal/mcpserver"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	log.SetPrefix("codebase-analyser-mcp: ")

	if err := mcpserver.New(nil).MCP().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
