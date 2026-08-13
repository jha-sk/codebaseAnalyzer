package store

import (
	"context"
	"errors"
	"testing"
)

func fixtureFindings(n int) []Finding {
	out := make([]Finding, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Finding{
			File: "main.go", Line: 10 + i, Tool: "gosec", RuleID: "G101",
			Category: "security", Severity: "high",
			Message: "hardcoded credential", Explanation: "leaks secrets",
		})
	}
	return out
}

func TestSaveRunStoresCountsToolsAndFindings(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	tools := []ToolStatus{
		{Name: "gosec"},
		{Name: "golangci-lint", Skipped: true, Error: "install failed"},
	}
	findings := []Finding{
		{File: "a.go", Line: 1, Tool: "gosec", RuleID: "G101", Category: "security", Severity: "critical", Message: "m1"},
		{File: "a.go", Line: 2, Tool: "gosec", RuleID: "G104", Category: "correctness", Severity: "low", Message: "m2"},
		{File: "b.go", Line: 3, Tool: "gosec", RuleID: "G104", Category: "correctness", Severity: "low", Message: "m3"},
	}
	runID, err := s.SaveRun(ctx, repo.ID, "main", "abc123", tools, findings)
	if err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	run, err := s.RunByID(ctx, runID)
	if err != nil {
		t.Fatalf("RunByID: %v", err)
	}
	if run.Counts["critical"] != 1 || run.Counts["low"] != 2 || run.Counts["high"] != 0 {
		t.Errorf("counts = %v, want critical=1 low=2 high=0", run.Counts)
	}
	if len(run.Tools) != 2 || !run.Tools[1].Skipped || run.Tools[1].Error != "install failed" {
		t.Errorf("tools round-tripped as %+v", run.Tools)
	}

	got, err := s.FindingsForRun(ctx, runID)
	if err != nil {
		t.Fatalf("FindingsForRun: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("FindingsForRun returned %d, want 3", len(got))
	}
	if got[0].File != "a.go" || got[0].Line != 1 || got[0].Severity != "critical" {
		t.Errorf("first finding = %+v", got[0])
	}
}

// FixPattern must round-trip through SaveRun/FindingsForRun and
// FindingsForRuns just like Explanation does: the dashboard's findings view
// shows "suggested fix" alongside "why it matters".
func TestSaveRunRoundTripsFixPattern(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	findings := []Finding{
		{File: "a.go", Line: 1, Tool: "gosec", RuleID: "G101", Category: "security", Severity: "high",
			Message: "hardcoded credential", Explanation: "leaks secrets", FixPattern: "load from a secrets manager instead"},
		{File: "b.go", Line: 2, Tool: "gosec", RuleID: "G104", Category: "correctness", Severity: "low",
			Message: "unhandled error", Explanation: "", FixPattern: ""},
	}
	runID, err := s.SaveRun(ctx, repo.ID, "main", "abc123", nil, findings)
	if err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	got, err := s.FindingsForRun(ctx, runID)
	if err != nil {
		t.Fatalf("FindingsForRun: %v", err)
	}
	if len(got) != 2 || got[0].FixPattern != "load from a secrets manager instead" {
		t.Fatalf("FindingsForRun = %+v, want the first finding's fix pattern to round-trip", got)
	}
	if got[1].FixPattern != "" {
		t.Errorf("second finding FixPattern = %q, want empty (no LLM fix pattern given)", got[1].FixPattern)
	}

	grouped, err := s.FindingsForRuns(ctx, []int64{runID})
	if err != nil {
		t.Fatalf("FindingsForRuns: %v", err)
	}
	if len(grouped[runID]) != 2 || grouped[runID][0].FixPattern != "load from a secrets manager instead" {
		t.Fatalf("FindingsForRuns = %+v, want the fix pattern to round-trip there too", grouped[runID])
	}
}

// The spec's headline ingest requirement: a CI retry pushing the same commit
// overwrites that run rather than creating a second one.
func TestSaveRunUpsertsOnSameCommit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	first, err := s.SaveRun(ctx, repo.ID, "main", "abc123", nil, fixtureFindings(3))
	if err != nil {
		t.Fatalf("first SaveRun: %v", err)
	}
	second, err := s.SaveRun(ctx, repo.ID, "main", "abc123", nil, fixtureFindings(1))
	if err != nil {
		t.Fatalf("second SaveRun: %v", err)
	}
	if first != second {
		t.Errorf("re-push created run %d, want the existing run %d", second, first)
	}

	runs, err := s.RunsForBranch(ctx, repo.ID, "main", 10)
	if err != nil {
		t.Fatalf("RunsForBranch: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("branch has %d runs after a re-push, want 1", len(runs))
	}
	if runs[0].Counts["high"] != 1 {
		t.Errorf("counts = %v, want the re-pushed high=1, not the original 3", runs[0].Counts)
	}
	got, _ := s.FindingsForRun(ctx, first)
	if len(got) != 1 {
		t.Errorf("run has %d findings after a re-push, want the 1 that replaced them", len(got))
	}
}

func TestRunsForBranchIsOldestFirstAndScoped(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	for _, c := range []string{"c1", "c2", "c3"} {
		if _, err := s.SaveRun(ctx, repo.ID, "main", c, nil, fixtureFindings(1)); err != nil {
			t.Fatalf("SaveRun %s: %v", c, err)
		}
	}
	if _, err := s.SaveRun(ctx, repo.ID, "feature", "d1", nil, fixtureFindings(1)); err != nil {
		t.Fatalf("SaveRun feature: %v", err)
	}

	runs, err := s.RunsForBranch(ctx, repo.ID, "main", 10)
	if err != nil {
		t.Fatalf("RunsForBranch: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs on main, want 3 (feature must not leak in)", len(runs))
	}
	if runs[0].CommitSHA != "c1" || runs[2].CommitSHA != "c3" {
		t.Errorf("order = %s,%s,%s, want oldest-first c1,c2,c3", runs[0].CommitSHA, runs[1].CommitSHA, runs[2].CommitSHA)
	}

	// limit keeps the NEWEST n, still returned oldest-first.
	runs, _ = s.RunsForBranch(ctx, repo.ID, "main", 2)
	if len(runs) != 2 || runs[0].CommitSHA != "c2" || runs[1].CommitSHA != "c3" {
		t.Errorf("limited runs = %+v, want the newest two c2,c3 in that order", runs)
	}
}

func TestBranchesSummarisesEachBranchOnce(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	if _, err := s.SaveRun(ctx, repo.ID, "main", "c1", nil, fixtureFindings(1)); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	if _, err := s.SaveRun(ctx, repo.ID, "main", "c2", nil, fixtureFindings(2)); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	last, err := s.SaveRun(ctx, repo.ID, "feature", "d1", nil, fixtureFindings(5))
	if err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	branches, err := s.Branches(ctx, repo.ID)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if len(branches) != 2 {
		t.Fatalf("got %d branches, want 2", len(branches))
	}
	if branches[0].Name != "feature" {
		t.Errorf("branches[0] = %q, want the most recently active branch 'feature'", branches[0].Name)
	}
	if branches[0].RunID != last || branches[0].Counts["high"] != 5 {
		t.Errorf("feature summary = %+v, want its latest run's counts", branches[0])
	}
	if branches[1].Name != "main" || branches[1].CommitSHA != "c2" {
		t.Errorf("main summary = %+v, want the latest commit c2", branches[1])
	}
}

func TestRunByIDMissing(t *testing.T) {
	s := testStore(t)
	if _, err := s.RunByID(context.Background(), 99999); err != ErrNotFound {
		t.Errorf("RunByID(missing) = %v, want ErrNotFound", err)
	}
}

func TestBranchesOrderIsTotalWhenTimestampsCollide(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	first, err := s.SaveRun(ctx, repo.ID, "alpha", "a1", nil, fixtureFindings(1))
	if err != nil {
		t.Fatalf("SaveRun alpha: %v", err)
	}
	second, err := s.SaveRun(ctx, repo.ID, "beta", "b1", nil, fixtureFindings(1))
	if err != nil {
		t.Fatalf("SaveRun beta: %v", err)
	}
	// Force both branches' latest runs onto the same instant: without a
	// tiebreak Postgres is then free to return them in either order.
	if _, err := s.db.ExecContext(ctx, `UPDATE runs SET pushed_at = now()`); err != nil {
		t.Fatalf("collide timestamps: %v", err)
	}

	for i := 0; i < 10; i++ {
		branches, err := s.Branches(ctx, repo.ID)
		if err != nil {
			t.Fatalf("Branches: %v", err)
		}
		if len(branches) != 2 {
			t.Fatalf("got %d branches, want 2", len(branches))
		}
		if branches[0].RunID != second || branches[1].RunID != first {
			t.Fatalf("run %d: order = %d,%d, want the newer run %d first every time",
				i, branches[0].RunID, branches[1].RunID, second)
		}
	}
}

// SaveRun is the single write path for findings; an unrecognised severity
// must be rejected there before anything is written, or runs.high/low/...
// silently stops summing to len(findings).
func TestSaveRunRejectsUnknownSeverity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	findings := []Finding{
		{File: "a.go", Line: 1, Tool: "gosec", RuleID: "G101", Category: "security", Severity: "blocker", Message: "m1"},
	}
	if _, err := s.SaveRun(ctx, repo.ID, "main", "abc123", nil, findings); !errors.Is(err, ErrInvalidFinding) {
		t.Fatalf("SaveRun err = %v, want ErrInvalidFinding", err)
	}

	runs, err := s.RunsForBranch(ctx, repo.ID, "main", 10)
	if err != nil {
		t.Fatalf("RunsForBranch: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("got %d runs after a rejected push, want 0 - nothing should have been written", len(runs))
	}
}

func TestSaveRunRejectsUnknownCategory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	findings := []Finding{
		{File: "a.go", Line: 1, Tool: "gosec", RuleID: "G101", Category: "style", Severity: "high", Message: "m1"},
	}
	if _, err := s.SaveRun(ctx, repo.ID, "main", "abc123", nil, findings); !errors.Is(err, ErrInvalidFinding) {
		t.Fatalf("SaveRun err = %v, want ErrInvalidFinding", err)
	}

	runs, err := s.RunsForBranch(ctx, repo.ID, "main", 10)
	if err != nil {
		t.Fatalf("RunsForBranch: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("got %d runs after a rejected push, want 0 - nothing should have been written", len(runs))
	}
}

func TestFindingsForRunsGroupsByRun(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	r1, _ := s.SaveRun(ctx, repo.ID, "main", "c1", nil, fixtureFindings(2))
	r2, _ := s.SaveRun(ctx, repo.ID, "main", "c2", nil, fixtureFindings(3))
	empty, _ := s.SaveRun(ctx, repo.ID, "main", "c3", nil, nil)

	got, err := s.FindingsForRuns(ctx, []int64{r1, r2, empty})
	if err != nil {
		t.Fatalf("FindingsForRuns: %v", err)
	}
	if len(got[r1]) != 2 || len(got[r2]) != 3 {
		t.Errorf("grouping = %d and %d findings, want 2 and 3", len(got[r1]), len(got[r2]))
	}
	if _, present := got[empty]; present {
		t.Error("a run with no findings must be absent from the map, not an empty entry")
	}
	if got[r1][0].Tool != "gosec" {
		t.Errorf("finding not fully scanned: %+v", got[r1][0])
	}

	none, err := s.FindingsForRuns(ctx, nil)
	if err != nil || len(none) != 0 {
		t.Errorf("FindingsForRuns(nil) = %v, %v; want an empty map and no error", none, err)
	}
}
