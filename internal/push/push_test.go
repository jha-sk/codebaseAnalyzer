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
	branch, commit, err := GitMeta("..")
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	if len(commit) < 7 {
		t.Errorf("commit = %q, want a full sha", commit)
	}
	if branch == "" {
		t.Error("branch is empty")
	}
}

func TestGitMetaFailsOutsideARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, _, err := GitMeta(t.TempDir()); err == nil {
		t.Error("GitMeta succeeded outside a git repository, want an error")
	}
}

// pickBranch is the pure fallback-chain logic behind GitMeta's branch
// resolution. Testing it directly, rather than through GitMeta, lets these
// cases run without a real (or detached) git checkout.
func TestPickBranchPrefersGithubRefName(t *testing.T) {
	got, err := pickBranch("release/1.2", "gitlab-branch", "main", "main")
	if err != nil {
		t.Fatalf("pickBranch: %v", err)
	}
	if got != "release/1.2" {
		t.Errorf("got %q, want GITHUB_REF_NAME to win", got)
	}
}

func TestPickBranchFallsBackToGitlabRefName(t *testing.T) {
	got, err := pickBranch("", "gitlab-branch", "main", "main")
	if err != nil {
		t.Fatalf("pickBranch: %v", err)
	}
	if got != "gitlab-branch" {
		t.Errorf("got %q, want CI_COMMIT_REF_NAME to win when GITHUB_REF_NAME is unset", got)
	}
}

// This is the regression case: a detached HEAD (what actions/checkout
// leaves by default) makes `git rev-parse --abbrev-ref HEAD` return the
// literal string "HEAD", which must never be stored as a branch name.
func TestPickBranchRejectsBareHEAD(t *testing.T) {
	got, err := pickBranch("", "", "HEAD", "")
	if err == nil {
		t.Fatalf("pickBranch returned %q, nil, want an error for a bare HEAD with no other candidate", got)
	}
}

func TestPickBranchFallsBackToBranchShowCurrentWhenRevParseIsHEAD(t *testing.T) {
	got, err := pickBranch("", "", "HEAD", "feature/x")
	if err != nil {
		t.Fatalf("pickBranch: %v", err)
	}
	if got != "feature/x" {
		t.Errorf("got %q, want the `git branch --show-current` fallback", got)
	}
}

func TestPickBranchUsesRevParseWhenNotHEAD(t *testing.T) {
	got, err := pickBranch("", "", "main", "")
	if err != nil {
		t.Fatalf("pickBranch: %v", err)
	}
	if got != "main" {
		t.Errorf("got %q, want main", got)
	}
}

func TestPickBranchErrorsWhenNothingResolves(t *testing.T) {
	if _, err := pickBranch("", "", "", ""); err == nil {
		t.Fatal("pickBranch succeeded with no usable candidate, want an error")
	}
}

// GitMeta itself must honor GITHUB_REF_NAME even when the underlying
// checkout is a normal branch (proving env-var precedence end to end, not
// just in the pure helper).
func TestGitMetaPrefersGithubRefNameEnvVar(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GITHUB_REF_NAME", "env-branch-override")
	branch, _, err := GitMeta("..")
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	if branch != "env-branch-override" {
		t.Errorf("branch = %q, want the GITHUB_REF_NAME override", branch)
	}
}
