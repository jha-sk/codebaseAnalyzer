package push

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSendPostsTheIngestEnvelope(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Write([]byte(`{"run_id":1}`))
	}))
	defer srv.Close()

	report := []byte(`{"summary":{"critical":0,"high":1,"medium":0,"low":0},"findings":[{"file":"a.go","line":1,"tool":"gosec","ruleID":"G101","category":"security","severity":"high","message":"m","explanation":"e"}]}`)
	tools := []ToolStatus{{Name: "gosec"}, {Name: "clippy", Skipped: true, Error: "install failed"}}

	if err := Send(context.Background(), srv.URL, "tok", "main", "abc123", tools, report); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/api/runs" {
		t.Errorf("posted to %q, want /api/runs", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q, want Bearer tok", gotAuth)
	}
	if gotBody["branch"] != "main" || gotBody["commit"] != "abc123" {
		t.Errorf("envelope metadata = %v", gotBody)
	}
	// The report must be nested as an object, not re-encoded as a string.
	rep, ok := gotBody["report"].(map[string]any)
	if !ok {
		t.Fatalf("report field = %T, want a JSON object", gotBody["report"])
	}
	findings, _ := rep["findings"].([]any)
	if len(findings) != 1 {
		t.Errorf("report.findings = %v, want the CLI report verbatim", rep["findings"])
	}
	toolList, _ := gotBody["tools"].([]any)
	if len(toolList) != 2 {
		t.Errorf("tools = %v, want both statuses", gotBody["tools"])
	}
}

func TestSendTrimsTrailingSlashOnBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	}))
	defer srv.Close()
	if err := Send(context.Background(), srv.URL+"/", "tok", "main", "c1", nil, []byte(`{}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/api/runs" {
		t.Errorf("path = %q, want /api/runs (no double slash)", gotPath)
	}
}

func TestSendSurfacesServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid ingest token"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := Send(context.Background(), srv.URL, "bad", "main", "c1", nil, []byte(`{}`))
	if err == nil {
		t.Fatal("Send returned nil for a 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want the status code in the message", err)
	}
}

func TestSendFailsFastWhenUnreachable(t *testing.T) {
	// Nothing is listening; the caller treats this as a warning, so all that
	// matters is that it returns an error promptly rather than hanging.
	start := time.Now()
	err := Send(context.Background(), "http://127.0.0.1:1", "tok", "main", "c1", nil, []byte(`{}`))
	if err == nil {
		t.Fatal("Send returned nil against a dead endpoint")
	}
	if time.Since(start) > 20*time.Second {
		t.Errorf("Send took %s; it must not hang a CI run", time.Since(start))
	}
}

func TestGitMetaReadsThisCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	remote, branch, commit, err := GitMeta("..")
	if err != nil {
		t.Skipf("not a git checkout with a remote: %v", err)
	}
	if len(commit) < 7 {
		t.Errorf("commit = %q, want a full sha", commit)
	}
	if branch == "" {
		t.Error("branch is empty")
	}
	if remote == "" {
		t.Error("remote is empty")
	}
}

func TestGitMetaFailsOutsideARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, _, _, err := GitMeta(t.TempDir()); err == nil {
		t.Error("GitMeta succeeded outside a git repository, want an error")
	}
}
