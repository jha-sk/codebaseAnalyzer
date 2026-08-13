package mcpserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/finding"
	"codebase-analyser/internal/mcpserver"
)

func callPush(t *testing.T, cs *mcp.ClientSession) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "push_to_dashboard", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res
}

func TestPushSendsLastRunToDashboard(t *testing.T) {
	type captured struct {
		path, auth string
		body       map[string]json.RawMessage
	}
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]json.RawMessage
		if err := json.Unmarshal(b, &body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		// EscapedPath, not Path: the repo id is percent-encoded into one
		// path segment, and Path would silently decode it back.
		got <- captured{path: r.URL.EscapedPath(), auth: r.Header.Get("Authorization"), body: body}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	t.Setenv("DASHBOARD_URL", srv.URL)
	t.Setenv("DASHBOARD_TOKEN", "secret-token")

	dir := goRepo(t)
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "fake", findings: manyFindings(120)}},
	})
	s.SetGitMeta(func(string) (string, string, string) {
		return "github.com/org/repo", "feature-x", "abc123"
	})
	cs := connect(t, s)

	callAnalyze(t, cs, map[string]any{"path": dir})
	if res := callPush(t, cs); res.IsError {
		t.Fatalf("push failed: %+v", res.Content)
	}

	c := <-got
	if want := "/api/repos/github.com%2Forg%2Frepo/runs"; c.path != want {
		t.Errorf("path = %q, want %q", c.path, want)
	}
	if c.auth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", c.auth, "Bearer secret-token")
	}
	var branch, commit string
	json.Unmarshal(c.body["branch"], &branch)
	json.Unmarshal(c.body["commit"], &commit)
	if branch != "feature-x" || commit != "abc123" {
		t.Errorf("branch/commit = %q/%q, want feature-x/abc123", branch, commit)
	}

	// The dashboard is a persistent record: it gets every finding, not the
	// 50 the agent-facing response is capped to.
	var rep struct {
		Findings []struct{} `json:"findings"`
	}
	if err := json.Unmarshal(c.body["report"], &rep); err != nil {
		t.Fatalf("report member is not the CLI's JSON report: %v", err)
	}
	if len(rep.Findings) != 120 {
		t.Errorf("pushed %d findings, want all 120 (uncapped)", len(rep.Findings))
	}
}

func TestPushWithoutPriorAnalysisIsToolError(t *testing.T) {
	t.Setenv("DASHBOARD_URL", "http://example.invalid")
	t.Setenv("DASHBOARD_TOKEN", "t")

	if res := callPush(t, connect(t, mcpserver.New(nil))); !res.IsError {
		t.Error("IsError = false, want true when no analysis has run yet")
	}
}

func TestPushWithoutConfigIsToolError(t *testing.T) {
	t.Setenv("DASHBOARD_URL", "")
	t.Setenv("DASHBOARD_TOKEN", "")

	dir := goRepo(t)
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "fake", findings: []finding.Finding{{File: "a.go", Severity: finding.SeverityLow, Category: finding.CategoryCorrectness}}}},
	})
	cs := connect(t, s)
	callAnalyze(t, cs, map[string]any{"path": dir})

	if res := callPush(t, cs); !res.IsError {
		t.Error("IsError = false, want true when DASHBOARD_URL/DASHBOARD_TOKEN are unset")
	}
}

func TestPushSurfacesServerRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unknown repo", http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("DASHBOARD_URL", srv.URL)
	t.Setenv("DASHBOARD_TOKEN", "t")

	dir := goRepo(t)
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "fake", findings: []finding.Finding{{File: "a.go", Severity: finding.SeverityLow, Category: finding.CategoryCorrectness}}}},
	})
	s.SetGitMeta(func(string) (string, string, string) { return "github.com/org/repo", "main", "deadbeef" })
	cs := connect(t, s)
	callAnalyze(t, cs, map[string]any{"path": dir})

	res := callPush(t, cs)
	if !res.IsError {
		t.Fatal("IsError = false, want true on a 404 from the dashboard")
	}
}
