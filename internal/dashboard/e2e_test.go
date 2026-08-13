package dashboard_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"testing/fstest"

	"codebase-analyser/internal/dashboard/api"
	"codebase-analyser/internal/dashboard/store"
	"codebase-analyser/internal/push"
)

const adminToken = "e2e-admin"

func TestCLIPushToDashboardRoundTrip(t *testing.T) {
	dsn := os.Getenv("DASHBOARD_TEST_DSN")
	if dsn == "" {
		t.Skip("DASHBOARD_TEST_DSN not set, skipping dashboard e2e test")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	if err := st.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}

	srv := httptest.NewServer(api.New(st, adminToken, fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")},
	}))
	defer srv.Close()

	repo, token, err := st.CreateRepo(ctx, "git@github.com:acme/widgets.git")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}

	// Push two runs through the CLI's own client, not hand-rolled HTTP.
	first := []byte(`{"summary":{"critical":0,"high":2,"medium":0,"low":0},"findings":[
		{"file":"a.go","line":1,"tool":"gosec","ruleID":"G101","category":"security","severity":"high","message":"hardcoded credential","explanation":"leaks secrets"},
		{"file":"b.go","line":7,"tool":"gosec","ruleID":"G104","category":"correctness","severity":"high","message":"unhandled error","explanation":"hides failures"}]}`)
	// second becomes "current" (the latest push) once both runs land, so the
	// fixPattern round-trip is asserted against a finding in here, not first.
	second := []byte(`{"summary":{"critical":1,"high":1,"medium":0,"low":0},"findings":[
		{"file":"a.go","line":3,"tool":"gosec","ruleID":"G101","category":"security","severity":"high","message":"hardcoded credential","explanation":"leaks secrets","fixPattern":"load from a secrets manager instead"},
		{"file":"c.go","line":9,"tool":"clippy","ruleID":"unwrap_used","category":"correctness","severity":"critical","message":"unwrap on Result","explanation":"panics in production"}]}`)

	tools := []push.ToolStatus{{Name: "gosec"}, {Name: "clippy", Skipped: true, Error: "install failed"}}
	if err := push.Send(ctx, srv.URL, token, "main", "commit-one", tools, first); err != nil {
		t.Fatalf("push run 1: %v", err)
	}
	if err := push.Send(ctx, srv.URL, token, "main", "commit-two", tools, second); err != nil {
		t.Fatalf("push run 2: %v", err)
	}
	// A CI retry of the second commit must not create a third run.
	if err := push.Send(ctx, srv.URL, token, "main", "commit-two", tools, second); err != nil {
		t.Fatalf("re-push run 2: %v", err)
	}

	req, _ := http.NewRequest("GET", srv.URL+"/api/repos/"+strconv.FormatInt(repo.ID, 10)+"/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET dashboard: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status %d", resp.StatusCode)
	}

	var got struct {
		Branch   string `json:"branch"`
		Branches []struct {
			Name string `json:"name"`
		} `json:"branches"`
		History []struct {
			CommitSHA string         `json:"commit_sha"`
			Counts    map[string]int `json:"counts"`
			New       int            `json:"new"`
			Fixed     int            `json:"fixed"`
		} `json:"history"`
		Current struct {
			New        int            `json:"new"`
			Fixed      int            `json:"fixed"`
			Categories map[string]int `json:"categories"`
			Findings   []struct {
				Tool        string `json:"tool"`
				RuleID      string `json:"ruleID"`
				Explanation string `json:"explanation"`
				FixPattern  string `json:"fixPattern"`
			} `json:"findings"`
			Run struct {
				Tools []struct {
					Name    string `json:"name"`
					Skipped bool   `json:"skipped"`
				} `json:"tools"`
			} `json:"run"`
		} `json:"current"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}

	if got.Branch != "main" || len(got.Branches) != 1 {
		t.Errorf("branch=%q branches=%v, want main only", got.Branch, got.Branches)
	}
	if len(got.History) != 2 {
		t.Fatalf("history has %d runs, want 2 (the retry must have overwritten, not appended)", len(got.History))
	}
	if got.History[0].CommitSHA != "commit-one" || got.History[1].CommitSHA != "commit-two" {
		t.Errorf("history order = %s,%s, want oldest first", got.History[0].CommitSHA, got.History[1].CommitSHA)
	}
	if got.History[1].Counts["critical"] != 1 || got.History[1].Counts["high"] != 1 {
		t.Errorf("second run counts = %v, want critical=1 high=1 recounted from findings", got.History[1].Counts)
	}
	if got.Current.New != 1 || got.Current.Fixed != 1 {
		t.Errorf("new=%d fixed=%d, want c.go new and b.go fixed (a.go moved line but is the same finding)",
			got.Current.New, got.Current.Fixed)
	}
	if got.Current.Categories["security"] != 1 || got.Current.Categories["correctness"] != 1 {
		t.Errorf("categories = %v", got.Current.Categories)
	}
	if len(got.Current.Findings) != 2 || got.Current.Findings[0].Explanation == "" {
		t.Errorf("findings did not survive the round trip with explanations: %+v", got.Current.Findings)
	}
	if got.Current.Findings[0].FixPattern != "load from a secrets manager instead" {
		t.Errorf("fixPattern did not survive the round trip: %+v", got.Current.Findings[0])
	}
	if len(got.Current.Run.Tools) != 2 || !got.Current.Run.Tools[1].Skipped {
		t.Errorf("tool statuses = %+v, want clippy recorded as skipped", got.Current.Run.Tools)
	}
}
