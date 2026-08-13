// Package mcpserver exposes the analyser pipeline over the Model Context
// Protocol, so any MCP-capable coding agent can run it as a tool. It is a
// second front door onto the same detect -> orchestrator engine the CLI
// drives; it does not reimplement any of it, and it never calls an LLM -
// its caller already is one.
package mcpserver

import (
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/orchestrator"
)

// Version is reported to MCP hosts during initialization. It is a var, not a
// const, so the release build can stamp the real tag into it with -ldflags -X.
var Version = "0.1.0"

type Server struct {
	adapters    map[string][]adapter.ToolAdapter
	maxFindings int
	mcp         *mcp.Server

	// gitMeta is a field, not a direct call, so tests can supply repo
	// identity without creating a real git repository. Same seam the
	// explain package uses for os.Getenv.
	gitMeta func(path string) (remote, branch, commit string)

	mu   sync.Mutex
	last *lastRun
}

// New builds a server. Passing nil adapters uses the real linters
// (orchestrator.DefaultAdapters); tests pass fakes so nothing shells out.
func New(adapters map[string][]adapter.ToolAdapter) *Server {
	if adapters == nil {
		adapters = orchestrator.DefaultAdapters
	}
	s := &Server{
		adapters:    adapters,
		maxFindings: DefaultMaxFindings,
		gitMeta:     gitMeta,
		mcp:         mcp.NewServer(&mcp.Implementation{Name: "codebase-analyser", Version: Version}, nil),
	}
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "analyze_codebase",
		Description: "Run static analysis (golangci-lint, gosec, govulncheck, clippy, cargo-audit) over a Go " +
			"and/or Rust repository and return production-safety findings. Blocks until the analysis " +
			"finishes; the first run on a repo may take several minutes because missing tools are " +
			"installed on demand.",
	}, s.analyze)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "push_to_dashboard",
		Description: "Push the most recent analyze_codebase result to the configured dashboard. " +
			"Requires DASHBOARD_URL and DASHBOARD_TOKEN in this server's environment; the token is " +
			"never returned to the caller. Takes no arguments.",
	}, s.push)
	return s
}

// MCP exposes the underlying protocol server so main can Run it on a
// transport and tests can connect to it in memory.
func (s *Server) MCP() *mcp.Server { return s.mcp }

// SetGitMeta overrides how repo identity is read. Tests use it to avoid
// depending on a real git checkout.
func (s *Server) SetGitMeta(f func(path string) (remote, branch, commit string)) { s.gitMeta = f }
