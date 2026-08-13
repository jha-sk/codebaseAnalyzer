package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"codebase-analyser/internal/dashboard/store"
)

// pushRun sends one run through the real ingest endpoint, so these tests
// exercise the same path CI does.
func pushRun(t *testing.T, srv *httptest.Server, token, branch, commit string, findings []store.Finding, tools []store.ToolStatus) {
	t.Helper()
	if findings == nil {
		findings = []store.Finding{}
	}
	body := map[string]any{
		"branch": branch, "commit": commit, "tools": tools,
		"report": map[string]any{"findings": findings},
	}
	resp, out := do(t, srv, "POST", "/api/runs", token, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("push %s@%s: status %d, body %s", branch, commit, resp.StatusCode, out)
	}
}

func sev(severity, file string, line int, rule string) store.Finding {
	return store.Finding{File: file, Line: line, Tool: "gosec", RuleID: rule,
		Category: "security", Severity: severity, Message: rule + " on " + file}
}

func getDashboard(t *testing.T, srv *httptest.Server, repoID int64, query string) dashboardResponse {
	t.Helper()
	resp, body := do(t, srv, "GET", "/api/repos/"+strconv.FormatInt(repoID, 10)+"/dashboard"+query, testAdminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard: status %d, body %s", resp.StatusCode, body)
	}
	var out dashboardResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode dashboard: %v (body %s)", err, body)
	}
	return out
}

func TestDashboardDefaultsToMainAndAssemblesEveryView(t *testing.T) {
	srv, _ := newTestServer(t)
	repoID, token := registerRepo(t, srv, "github.com/acme/widgets")

	pushRun(t, srv, token, "feature", "f1", []store.Finding{sev("low", "z.go", 1, "G104")}, nil)
	pushRun(t, srv, token, "main", "c1", []store.Finding{
		sev("high", "a.go", 1, "G101"),
		sev("high", "a.go", 2, "G102"),
		sev("low", "b.go", 3, "G104"),
	}, nil)
	pushRun(t, srv, token, "main", "c2", []store.Finding{
		sev("high", "a.go", 1, "G101"), // carried over
		{File: "c.go", Line: 9, Tool: "clippy", RuleID: "unwrap_used", // new, other category
			Category: "correctness", Severity: "critical", Message: "unwrap"},
	}, []store.ToolStatus{{Name: "gosec"}, {Name: "clippy", Skipped: true, Error: "install failed"}})

	got := getDashboard(t, srv, repoID, "")

	if got.Branch != "main" {
		t.Errorf("served branch %q, want the default 'main'", got.Branch)
	}
	if len(got.Branches) != 2 {
		t.Errorf("branches = %+v, want both main and feature", got.Branches)
	}
	if len(got.History) != 2 || got.History[0].CommitSHA != "c1" {
		t.Fatalf("history = %+v, want c1 then c2 (oldest first)", got.History)
	}
	if got.Current == nil {
		t.Fatal("current is null on a branch that has runs")
	}
	if got.Current.Run.CommitSHA != "c2" {
		t.Errorf("current run = %s, want the latest c2", got.Current.Run.CommitSHA)
	}
	if got.Current.New != 1 || got.Current.Fixed != 2 {
		t.Errorf("new=%d fixed=%d, want new=1 (c.go) fixed=2 (a.go:2, b.go)", got.Current.New, got.Current.Fixed)
	}
	if got.Current.Deltas["critical"] != 1 || got.Current.Deltas["low"] != -1 {
		t.Errorf("deltas = %v, want critical +1 and low -1 vs c1", got.Current.Deltas)
	}
	if got.Current.Categories["security"] != 1 || got.Current.Categories["correctness"] != 1 {
		t.Errorf("categories = %v", got.Current.Categories)
	}
	if len(got.Current.TopFiles) == 0 || got.Current.TopFiles[0].Count != 1 {
		t.Errorf("top_files = %+v", got.Current.TopFiles)
	}
	if len(got.Current.Findings) != 2 {
		t.Errorf("findings = %d, want 2", len(got.Current.Findings))
	}
	if len(got.Current.Run.Tools) != 2 || !got.Current.Run.Tools[1].Skipped {
		t.Errorf("tool statuses = %+v, want clippy marked skipped", got.Current.Run.Tools)
	}
	if got.Current.Health <= 0 || got.Current.Health > 100 {
		t.Errorf("health = %d, want a 1..100 score", got.Current.Health)
	}
}

func TestDashboardHonoursBranchQuery(t *testing.T) {
	srv, _ := newTestServer(t)
	repoID, token := registerRepo(t, srv, "github.com/acme/widgets")
	pushRun(t, srv, token, "main", "c1", []store.Finding{sev("high", "a.go", 1, "G101")}, nil)
	pushRun(t, srv, token, "feature", "f1", []store.Finding{sev("low", "z.go", 1, "G104")}, nil)

	got := getDashboard(t, srv, repoID, "?branch=feature")
	if got.Branch != "feature" || got.Current == nil || got.Current.Run.CommitSHA != "f1" {
		t.Fatalf("branch query ignored: %+v", got)
	}
	if got.Current.Run.Counts["low"] != 1 || got.Current.Run.Counts["high"] != 0 {
		t.Errorf("counts = %v, want only the feature branch's findings", got.Current.Run.Counts)
	}
}

func TestDashboardEmptyAndMissingRepo(t *testing.T) {
	srv, _ := newTestServer(t)
	repoID, _ := registerRepo(t, srv, "github.com/acme/widgets")

	got := getDashboard(t, srv, repoID, "")
	if got.Current != nil || len(got.Branches) != 0 || len(got.History) != 0 {
		t.Errorf("a repo with no runs returned %+v, want empty views and a null current", got)
	}

	resp, _ := do(t, srv, "GET", "/api/repos/9999/dashboard", testAdminToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing repo: status %d, want 404", resp.StatusCode)
	}
	resp, _ = do(t, srv, "GET", "/api/repos/"+strconv.FormatInt(repoID, 10)+"/dashboard", "wrong", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad admin token: status %d, want 401", resp.StatusCode)
	}
}

func TestRunDrillDown(t *testing.T) {
	srv, _ := newTestServer(t)
	repoID, token := registerRepo(t, srv, "github.com/acme/widgets")
	pushRun(t, srv, token, "main", "c1", []store.Finding{sev("high", "a.go", 7, "G101")}, nil)
	pushRun(t, srv, token, "main", "c2", nil, nil)

	dash := getDashboard(t, srv, repoID, "")
	older := dash.History[0].RunID

	resp, body := do(t, srv, "GET", "/api/runs/"+strconv.FormatInt(older, 10), testAdminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("run detail: status %d, body %s", resp.StatusCode, body)
	}
	var out runResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Run.CommitSHA != "c1" || len(out.Findings) != 1 || out.Findings[0].Line != 7 {
		t.Errorf("drill-down = %+v", out)
	}

	resp, _ = do(t, srv, "GET", "/api/runs/9999", testAdminToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing run: status %d, want 404", resp.StatusCode)
	}
}

func TestDashboardComparesAgainstTheImmediatelyPreviousRun(t *testing.T) {
	srv, _ := newTestServer(t)
	repoID, token := registerRepo(t, srv, "github.com/acme/widgets")

	// Several runs of history, then a final run that fixes one finding and
	// introduces another. The deltas must reflect the run immediately before
	// it, not the start of the branch and not a truncated window.
	for _, c := range []string{"c1", "c2", "c3"} {
		pushRun(t, srv, token, "main", c, []store.Finding{
			sev("high", "a.go", 1, "G101"),
			sev("high", "b.go", 2, "G102"),
		}, nil)
	}
	pushRun(t, srv, token, "main", "c4", []store.Finding{
		sev("high", "a.go", 1, "G101"), // carried over
		sev("low", "c.go", 3, "G104"),  // new
	}, nil)

	got := getDashboard(t, srv, repoID, "")
	if got.Current == nil {
		t.Fatal("current is null")
	}
	if got.Current.New != 1 || got.Current.Fixed != 1 {
		t.Errorf("new=%d fixed=%d, want 1 and 1 against c3 specifically", got.Current.New, got.Current.Fixed)
	}
	if got.Current.Deltas["high"] != -1 || got.Current.Deltas["low"] != 1 {
		t.Errorf("deltas = %v, want high -1 and low +1 versus c3", got.Current.Deltas)
	}
}
