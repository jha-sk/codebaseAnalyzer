package api

import (
	"testing"
	"time"

	"codebase-analyser/internal/dashboard/store"
)

func counts(critical, high, medium, low int) map[string]int {
	return map[string]int{"critical": critical, "high": high, "medium": medium, "low": low}
}

func TestHealthScoreWeightsBySeverity(t *testing.T) {
	if got := HealthScore(counts(0, 0, 0, 0), nil); got != 100 {
		t.Errorf("clean run scored %d, want 100", got)
	}
	// One critical must cost more than one low.
	crit := HealthScore(counts(1, 0, 0, 0), nil)
	low := HealthScore(counts(0, 0, 0, 1), nil)
	if crit >= low {
		t.Errorf("critical scored %d and low scored %d; critical must cost more", crit, low)
	}
	if got := HealthScore(counts(50, 50, 50, 50), nil); got != 0 {
		t.Errorf("catastrophic run scored %d, want the floor 0", got)
	}
}

func TestHealthScoreIsTrendAdjusted(t *testing.T) {
	cur := counts(0, 2, 0, 0)
	improving := HealthScore(cur, counts(0, 5, 0, 0))
	flat := HealthScore(cur, counts(0, 2, 0, 0))
	worsening := HealthScore(cur, counts(0, 1, 0, 0))
	if !(improving > flat && flat > worsening) {
		t.Errorf("improving=%d flat=%d worsening=%d; want improving > flat > worsening", improving, flat, worsening)
	}
	if HealthScore(cur, nil) != flat {
		t.Error("a first-ever run must score the same as a flat trend, not be penalised")
	}
	// The adjustment must never push the score outside 0..100.
	if got := HealthScore(counts(0, 0, 0, 0), counts(0, 9, 0, 0)); got != 100 {
		t.Errorf("perfect run after a bad one scored %d, want the ceiling 100", got)
	}
}

func TestHealthScoreTrendFollowsSeverityNotCount(t *testing.T) {
	// Two lows becoming one critical is a regression, even though the raw
	// finding count went down. It must not outscore a run that never changed.
	escalated := HealthScore(counts(1, 0, 0, 0), counts(0, 0, 0, 2))
	unchanged := HealthScore(counts(1, 0, 0, 0), counts(1, 0, 0, 0))
	if escalated >= unchanged {
		t.Errorf("escalated=%d unchanged=%d; escalating low->critical must score below standing still",
			escalated, unchanged)
	}
}

func TestDiffCountsNewAndFixed(t *testing.T) {
	prev := []store.Finding{
		{File: "a.go", Line: 10, Tool: "gosec", RuleID: "G101", Message: "m1"},
		{File: "b.go", Line: 20, Tool: "gosec", RuleID: "G104", Message: "m2"},
	}
	cur := []store.Finding{
		// same finding, line drifted by an unrelated edit above it - not new.
		{File: "a.go", Line: 14, Tool: "gosec", RuleID: "G101", Message: "m1"},
		{File: "c.go", Line: 1, Tool: "clippy", RuleID: "unwrap_used", Message: "m3"},
	}
	added, fixed := Diff(prev, cur)
	if added != 1 {
		t.Errorf("added = %d, want 1 (only c.go is new)", added)
	}
	if fixed != 1 {
		t.Errorf("fixed = %d, want 1 (only b.go went away)", fixed)
	}

	if a, f := Diff(nil, cur); a != 2 || f != 0 {
		t.Errorf("first-ever run: added=%d fixed=%d, want 2 and 0", a, f)
	}
	if a, f := Diff(prev, nil); a != 0 || f != 2 {
		t.Errorf("everything fixed: added=%d fixed=%d, want 0 and 2", a, f)
	}
}

func TestCategoryCountsAlwaysHasFourKeys(t *testing.T) {
	got := CategoryCounts([]store.Finding{
		{Category: "security"}, {Category: "security"}, {Category: "concurrency"},
		{Category: "nonsense"}, // unknown categories are ignored, not counted
	})
	if len(got) != 4 {
		t.Fatalf("got %d keys (%v), want exactly the four spec categories", len(got), got)
	}
	if got["security"] != 2 || got["concurrency"] != 1 || got["correctness"] != 0 || got["operational"] != 0 {
		t.Errorf("counts = %v", got)
	}
}

func TestTopFilesRanksAndTruncates(t *testing.T) {
	fs := []store.Finding{
		{File: "b.go"}, {File: "b.go"}, {File: "b.go"},
		{File: "a.go"}, {File: "a.go"},
		{File: "c.go"},
		{File: "d.go"},
	}
	got := TopFiles(fs, 3)
	if len(got) != 3 {
		t.Fatalf("got %d files, want 3", len(got))
	}
	if got[0].File != "b.go" || got[0].Count != 3 || got[1].File != "a.go" {
		t.Errorf("ranking = %+v, want b.go(3) then a.go(2)", got)
	}
	// c.go and d.go tie at 1; the tie must break alphabetically so the view is
	// stable across renders rather than shuffling with map iteration.
	if got[2].File != "c.go" {
		t.Errorf("tie broke to %q, want the alphabetically first 'c.go'", got[2].File)
	}
	if len(TopFiles(nil, 3)) != 0 {
		t.Error("TopFiles(nil) must be empty, not nil-panicking")
	}
	if got := TopFiles(fs, -1); got == nil || len(got) != 0 {
		t.Errorf("TopFiles(fs, -1) = %+v, want a non-nil empty slice, not a panic", got)
	}
}

func TestPickDefaultBranch(t *testing.T) {
	now := time.Now()
	branches := []store.Branch{
		{Name: "feature-x", LastRunAt: now},
		{Name: "master", LastRunAt: now.Add(-time.Hour)},
		{Name: "main", LastRunAt: now.Add(-2 * time.Hour)},
	}
	if got := PickDefaultBranch(branches); got != "main" {
		t.Errorf("got %q, want main even though it is not the most recent", got)
	}
	if got := PickDefaultBranch(branches[:2]); got != "master" {
		t.Errorf("got %q, want master when there is no main", got)
	}
	if got := PickDefaultBranch(branches[:1]); got != "feature-x" {
		t.Errorf("got %q, want the most recent branch when neither main nor master exists", got)
	}
	if got := PickDefaultBranch(nil); got != "" {
		t.Errorf("got %q, want empty for a repo that has never pushed", got)
	}
}
