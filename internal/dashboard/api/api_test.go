package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"testing/fstest"

	"codebase-analyser/internal/dashboard/store"
)

const testAdminToken = "admin-secret"

// newTestServer boots the real handler over the real test database.
func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	dsn := os.Getenv("DASHBOARD_TEST_DSN")
	if dsn == "" {
		t.Skip("DASHBOARD_TEST_DSN not set, skipping Postgres-backed test")
	}
	st, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.Reset(context.Background()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	srv := httptest.NewServer(New(st, testAdminToken, fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>dash</title>")},
	}))
	t.Cleanup(func() { srv.Close(); st.Close() })
	return srv, st
}

func do(t *testing.T, srv *httptest.Server, method, path, token string, body any) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req, err := http.NewRequest(method, srv.URL+path, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out := new(bytes.Buffer)
	out.ReadFrom(resp.Body)
	return resp, out.Bytes()
}

func registerRepo(t *testing.T, srv *httptest.Server, remote string) (int64, string) {
	t.Helper()
	resp, body := do(t, srv, "POST", "/api/admin/repos", testAdminToken,
		map[string]string{"remote_url": remote})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: status %d, body %s", resp.StatusCode, body)
	}
	var out struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return out.ID, out.Token
}

func TestAdminEndpointsRequireAdminToken(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, tc := range []struct{ method, path, token string }{
		{"POST", "/api/admin/repos", ""},
		{"POST", "/api/admin/repos", "wrong"},
		{"GET", "/api/admin/repos", "wrong"},
	} {
		resp, _ := do(t, srv, tc.method, tc.path, tc.token, map[string]string{"remote_url": "github.com/a/b"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s with token %q: status %d, want 401", tc.method, tc.path, tc.token, resp.StatusCode)
		}
	}
}

func TestRegisterRepoReturnsTokenOnce(t *testing.T) {
	srv, _ := newTestServer(t)
	id, token := registerRepo(t, srv, "git@github.com:acme/widgets.git")
	if id == 0 || len(token) < 32 {
		t.Fatalf("register returned id=%d token=%q", id, token)
	}

	resp, body := do(t, srv, "GET", "/api/admin/repos", testAdminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status %d", resp.StatusCode)
	}
	if bytes.Contains(body, []byte(token)) {
		t.Error("the repo list leaked the ingest token; it must be shown only at registration")
	}
	if !bytes.Contains(body, []byte("github.com/acme/widgets")) {
		t.Errorf("list body = %s, want the normalized remote URL", body)
	}

	// Duplicate registration is a client error, not a 500.
	resp, _ = do(t, srv, "POST", "/api/admin/repos", testAdminToken,
		map[string]string{"remote_url": "https://github.com/acme/widgets"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate register: status %d, want 409", resp.StatusCode)
	}

	// Missing remote_url is a 400.
	resp, _ = do(t, srv, "POST", "/api/admin/repos", testAdminToken, map[string]string{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty remote_url: status %d, want 400", resp.StatusCode)
	}

	// A non-empty remote_url that still normalizes to nothing is bad input,
	// not a server fault. NormalizeRemoteURL(":::") == "" - no hostname can be
	// pulled out of it.
	resp, body = do(t, srv, "POST", "/api/admin/repos", testAdminToken,
		map[string]string{"remote_url": ":::"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unnormalizable remote_url: status %d, body %s, want 400", resp.StatusCode, body)
	}
}

func TestIngestStoresTheRun(t *testing.T) {
	srv, st := newTestServer(t)
	repoID, token := registerRepo(t, srv, "github.com/acme/widgets")

	payload := map[string]any{
		"branch": "main",
		"commit": "abc123",
		"tools":  []map[string]any{{"name": "gosec"}, {"name": "clippy", "skipped": true, "error": "install failed"}},
		"report": map[string]any{
			"summary": map[string]int{"critical": 99, "high": 99, "medium": 99, "low": 99}, // ignored on purpose
			"findings": []map[string]any{
				{"file": "a.go", "line": 4, "tool": "gosec", "ruleID": "G101",
					"category": "security", "severity": "critical", "message": "m", "explanation": "e"},
			},
		},
	}
	resp, body := do(t, srv, "POST", "/api/runs", token, payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ingest: status %d, body %s", resp.StatusCode, body)
	}

	runs, err := st.RunsForBranch(context.Background(), repoID, "main", 10)
	if err != nil {
		t.Fatalf("RunsForBranch: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("stored %d runs, want 1", len(runs))
	}
	if runs[0].Counts["critical"] != 1 || runs[0].Counts["high"] != 0 {
		t.Errorf("counts = %v; the server must recount from findings, not trust the pushed summary", runs[0].Counts)
	}
	if len(runs[0].Tools) != 2 || !runs[0].Tools[1].Skipped {
		t.Errorf("tool statuses = %+v", runs[0].Tools)
	}
}

func TestIngestRejectsBadTokensAndInput(t *testing.T) {
	srv, _ := newTestServer(t)
	_, token := registerRepo(t, srv, "github.com/acme/widgets")
	good := map[string]any{"branch": "main", "commit": "abc123",
		"report": map[string]any{"findings": []any{}}}

	resp, _ := do(t, srv, "POST", "/api/runs", "not-a-token", good)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bogus ingest token: status %d, want 401", resp.StatusCode)
	}
	resp, _ = do(t, srv, "POST", "/api/runs", testAdminToken, good)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("admin token used for ingest: status %d, want 401 (it is not an ingest token)", resp.StatusCode)
	}
	resp, _ = do(t, srv, "POST", "/api/runs", token,
		map[string]any{"commit": "abc123", "report": map[string]any{"findings": []any{}}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing branch: status %d, want 400", resp.StatusCode)
	}
}

// TestIngestRejectsInvalidFinding covers the Task 2 sentinel: a finding
// carrying an unrecognised severity must fail with 400 (bad client input),
// never 500 - a 500 would tell CI to retry a push that can never succeed.
func TestIngestRejectsInvalidFinding(t *testing.T) {
	srv, _ := newTestServer(t)
	_, token := registerRepo(t, srv, "github.com/acme/widgets")

	payload := map[string]any{
		"branch": "main",
		"commit": "abc123",
		"report": map[string]any{
			"findings": []map[string]any{
				{"file": "a.go", "line": 4, "tool": "gosec", "ruleID": "G101",
					"category": "security", "severity": "blocker", "message": "m", "explanation": "e"},
			},
		},
	}
	resp, body := do(t, srv, "POST", "/api/runs", token, payload)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid severity: status %d, body %s, want 400", resp.StatusCode, body)
	}
}

// TestIngestTokenCannotWriteAnotherRepo makes the cross-repo isolation
// property (currently held only because ingestRequest has no repo id field to
// attack) into an actual regression test, ahead of Task 5 adding more routes
// to this package.
func TestIngestTokenCannotWriteAnotherRepo(t *testing.T) {
	srv, st := newTestServer(t)
	_, tokenA := registerRepo(t, srv, "github.com/acme/alpha")
	repoB, _ := registerRepo(t, srv, "github.com/acme/beta")

	body := map[string]any{"branch": "main", "commit": "c1",
		"report": map[string]any{"findings": []any{}}}
	if resp, out := do(t, srv, "POST", "/api/runs", tokenA, body); resp.StatusCode != http.StatusOK {
		t.Fatalf("push with repo A's token: status %d, body %s", resp.StatusCode, out)
	}

	runs, err := st.RunsForBranch(context.Background(), repoB, "main", 10)
	if err != nil {
		t.Fatalf("RunsForBranch: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("repo B has %d runs; repo A's token must not be able to write them", len(runs))
	}
}

func TestTokenRegenerateAndRevoke(t *testing.T) {
	srv, _ := newTestServer(t)
	id, old := registerRepo(t, srv, "github.com/acme/widgets")
	run := map[string]any{"branch": "main", "commit": "c1", "report": map[string]any{"findings": []any{}}}

	resp, body := do(t, srv, "POST", "/api/admin/repos/"+itoa(id)+"/token", testAdminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("regenerate: status %d, body %s", resp.StatusCode, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	json.Unmarshal(body, &out)

	if resp, _ := do(t, srv, "POST", "/api/runs", old, run); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("old token after regenerate: status %d, want 401", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "POST", "/api/runs", out.Token, run); resp.StatusCode != http.StatusOK {
		t.Errorf("new token: status %d, want 200", resp.StatusCode)
	}

	if resp, _ := do(t, srv, "DELETE", "/api/admin/repos/"+itoa(id), testAdminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete: status %d, want 204", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "POST", "/api/runs", out.Token, run); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("token after repo delete: status %d, want 401", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "DELETE", "/api/admin/repos/9999", testAdminToken, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("delete missing repo: status %d, want 404", resp.StatusCode)
	}
}

func TestServesEmbeddedIndex(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, body := do(t, srv, "GET", "/", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: status %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte("<title>dash</title>")) {
		t.Errorf("GET / served %s, want the embedded index.html", body)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

// A dashboard deployed with an empty DASHBOARD_ADMIN_TOKEN must authenticate
// nobody. Without the explicit empty check, ConstantTimeCompare("", "") returns
// 1 and every unauthenticated request would be treated as an admin.
func TestEmptyAdminTokenAuthenticatesNobody(t *testing.T) {
	dsn := os.Getenv("DASHBOARD_TEST_DSN")
	if dsn == "" {
		t.Skip("DASHBOARD_TEST_DSN not set, skipping Postgres-backed test")
	}
	st, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	srv := httptest.NewServer(New(st, "", fstest.MapFS{}))
	t.Cleanup(srv.Close)

	for _, token := range []string{"", "anything"} {
		resp, _ := do(t, srv, "GET", "/api/admin/repos", token, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("empty admin token, request with %q: status %d, want 401", token, resp.StatusCode)
		}
	}
}
