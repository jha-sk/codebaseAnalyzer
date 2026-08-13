package mcpserver_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/finding"
	"codebase-analyser/internal/mcpserver"
)

var errFake = errors.New("fake tool failure")

// fakeAdapter stands in for a real linter so tests never shell out.
type fakeAdapter struct {
	name     string
	findings []finding.Finding
	err      error
}

func (f fakeAdapter) Name() string         { return f.name }
func (f fakeAdapter) CheckInstalled() bool { return true }
func (f fakeAdapter) Install() error       { return nil }
func (f fakeAdapter) Run(path string) ([]finding.Finding, error) {
	return f.findings, f.err
}

// goRepo writes a minimal Go project so detect.Detect finds exactly one.
func goRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// connect wires a client to s over an in-memory transport. The server must be
// connected before the client: the client initializes the session on connect.
func connect(t *testing.T, s *mcpserver.Server) *mcp.ClientSession {
	t.Helper()
	ctx := t.Context()
	clientT, serverT := mcp.NewInMemoryTransports()
	ss, err := s.MCP().Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })

	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	cs, err := c.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// callAnalyze calls analyze_codebase and decodes its structured output.
func callAnalyze(t *testing.T, cs *mcp.ClientSession, args map[string]any) (mcpserver.AnalyzeOutput, *mcp.CallToolResult) {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "analyze_codebase", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var out mcpserver.AnalyzeOutput
	if res.StructuredContent != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal structured content: %v", err)
		}
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("decode structured content: %v", err)
		}
	}
	return out, res
}

func TestAnalyzeReturnsFindings(t *testing.T) {
	dir := goRepo(t)
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "fake", findings: []finding.Finding{
			{File: "a.go", Line: 3, Tool: "fake", RuleID: "R1", Category: finding.CategorySecurity, Severity: finding.SeverityCritical, Message: "boom"},
			{File: "b.go", Line: 7, Tool: "fake", RuleID: "R2", Category: finding.CategoryCorrectness, Severity: finding.SeverityLow, Message: "meh"},
		}}},
	})

	out, res := callAnalyze(t, connect(t, s), map[string]any{"path": dir})

	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	if out.Total != 2 {
		t.Errorf("Total = %d, want 2", out.Total)
	}
	if len(out.Findings) != 2 {
		t.Fatalf("len(Findings) = %d, want 2", len(out.Findings))
	}
	if out.Summary["critical"] != 1 || out.Summary["low"] != 1 {
		t.Errorf("Summary = %v, want 1 critical and 1 low", out.Summary)
	}
	if out.Incomplete {
		t.Error("Incomplete = true, want false")
	}
}

func TestAnalyzeReportsSkippedTool(t *testing.T) {
	dir := goRepo(t)
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "broken", err: errFake}},
	})

	out, res := callAnalyze(t, connect(t, s), map[string]any{"path": dir})

	if res.IsError {
		t.Fatalf("a skipped tool must not fail the call: %+v", res.Content)
	}
	if !out.Incomplete {
		t.Error("Incomplete = false, want true when a tool was skipped")
	}
	if len(out.SkippedTools) != 1 || out.SkippedTools[0].Tool != "broken" {
		t.Errorf("SkippedTools = %+v, want one entry for \"broken\"", out.SkippedTools)
	}
}

func TestAnalyzeNoProjectIsToolError(t *testing.T) {
	s := mcpserver.New(nil)

	_, res := callAnalyze(t, connect(t, s), map[string]any{"path": t.TempDir()})

	if !res.IsError {
		t.Fatal("IsError = false, want true when no Go/Rust project is present")
	}
}

func TestToolsAreDiscoverable(t *testing.T) {
	cs := connect(t, mcpserver.New(nil))

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "analyze_codebase" {
			found = true
		}
	}
	if !found {
		t.Errorf("analyze_codebase not advertised; got %+v", tools.Tools)
	}
}
