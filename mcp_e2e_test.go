package e2e

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// buildMCPServer compiles the server binary into a temp dir and returns its
// path.
func buildMCPServer(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "codebase-analyser-mcp")
	out, err := exec.Command("go", "build", "-o", bin, "codebase-analyser/cmd/codebase-analyser-mcp").CombinedOutput()
	if err != nil {
		t.Fatalf("build server: %v\n%s", err, out)
	}
	return bin
}

// TestMCPStdioConformance spawns the real binary and drives it over stdio,
// the exact path an MCP host takes.
func TestMCPStdioConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and spawns a binary")
	}
	ctx := t.Context()
	bin := buildMCPServer(t)

	client := mcp.NewClient(&mcp.Implementation{Name: "conformance", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(bin)}, nil)
	if err != nil {
		t.Fatalf("connect over stdio: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"analyze_codebase", "push_to_dashboard"} {
		if !names[want] {
			t.Errorf("tool %q not advertised; got %v", want, names)
		}
	}

	// An empty directory has no go.mod/Cargo.toml, so this exercises the
	// full request/response path without running a single linter.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "analyze_codebase",
		Arguments: map[string]any{"path": t.TempDir()},
	})
	if err != nil {
		t.Fatalf("CallTool(analyze_codebase): %v", err)
	}
	if !res.IsError {
		t.Error("analyze_codebase on an empty dir: IsError = false, want true")
	}
	if got := textOf(res); !strings.Contains(got, "no Go or Rust project") {
		t.Errorf("error text = %q, want it to name the missing project", got)
	}

	// DASHBOARD_URL/TOKEN are unset in this process, so the push must fail
	// cleanly rather than hang or crash the server.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "push_to_dashboard", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool(push_to_dashboard): %v", err)
	}
	if !res.IsError {
		t.Error("push_to_dashboard with no prior analysis: IsError = false, want true")
	}

	// The session is still usable after two tool errors: an error result is
	// a normal response, not a broken connection.
	if _, err := session.ListTools(ctx, nil); err != nil {
		t.Errorf("session unusable after tool errors: %v", err)
	}
}

func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
