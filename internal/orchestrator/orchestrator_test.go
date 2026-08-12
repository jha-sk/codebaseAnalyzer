package orchestrator

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/detect"
	"codebase-analyser/internal/finding"
)

type fakeAdapter struct {
	name         string
	installed    bool
	findings     []finding.Finding
	runErr       error
	installErr   error
	installCalls *int32
	panics       bool
}

func (f fakeAdapter) Name() string         { return f.name }
func (f fakeAdapter) CheckInstalled() bool { return f.installed }
func (f fakeAdapter) Install() error {
	if f.installCalls != nil {
		atomic.AddInt32(f.installCalls, 1)
	}
	return f.installErr
}
func (f fakeAdapter) Run(path string) ([]finding.Finding, error) {
	if f.panics {
		panic("malformed tool output: index out of range")
	}
	return f.findings, f.runErr
}

func TestRun_collectsFindingsAndSkips(t *testing.T) {
	projects := []detect.Project{{Path: "/repo", Language: "go"}}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {
			fakeAdapter{name: "ok-tool", installed: true, findings: []finding.Finding{{Tool: "ok-tool", RuleID: "R1"}}},
			fakeAdapter{name: "broken-tool", installed: true, runErr: errors.New("boom")},
		},
	}

	results := Run(projects, adapters)

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	var okResult, brokenResult ToolResult
	for _, r := range results {
		switch r.Tool {
		case "ok-tool":
			okResult = r
		case "broken-tool":
			brokenResult = r
		}
	}
	if okResult.Skipped || len(okResult.Findings) != 1 {
		t.Errorf("ok-tool result = %+v", okResult)
	}
	if !brokenResult.Skipped || brokenResult.Error == nil {
		t.Errorf("broken-tool result = %+v, want Skipped=true with Error", brokenResult)
	}
}

// A panic inside one adapter's Run (e.g. a bug parsing malformed tool
// output) must be isolated to that tool's result, not crash the process or
// stop other adapters dispatched in the same Run call from completing.
func TestRun_panicIsolation(t *testing.T) {
	projects := []detect.Project{{Path: "/repo", Language: "go"}}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {
			fakeAdapter{name: "panicky-tool", installed: true, panics: true},
			fakeAdapter{name: "healthy-tool", installed: true, findings: []finding.Finding{{Tool: "healthy-tool", RuleID: "R2"}}},
		},
	}

	results := Run(projects, adapters)

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	var panicResult, healthyResult ToolResult
	for _, r := range results {
		switch r.Tool {
		case "panicky-tool":
			panicResult = r
		case "healthy-tool":
			healthyResult = r
		}
	}
	if !panicResult.Skipped || panicResult.Error == nil || !strings.Contains(panicResult.Error.Error(), "panic") {
		t.Errorf("panicky-tool result = %+v, want Skipped=true with an error mentioning the panic", panicResult)
	}
	// The real point: a sibling adapter in the same Run call still finishes
	// normally, proving the panic was isolated rather than taking the
	// process (and every other in-flight adapter) down with it.
	if healthyResult.Skipped || len(healthyResult.Findings) != 1 {
		t.Errorf("healthy-tool result = %+v, want unaffected by the sibling panic", healthyResult)
	}
}

// A tool that isn't installed gets auto-installed once, then runs normally.
func TestRun_installsWhenMissing(t *testing.T) {
	var calls int32
	projects := []detect.Project{{Path: "/repo", Language: "go"}}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {
			fakeAdapter{name: "needs-install", installed: false, installCalls: &calls, findings: []finding.Finding{{Tool: "needs-install"}}},
		},
	}

	results := Run(projects, adapters)

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Skipped || len(results[0].Findings) != 1 {
		t.Errorf("result = %+v, want successful install followed by a normal run", results[0])
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("Install called %d times, want 1", got)
	}
}

// A tool whose install fails is skipped with that error, and does not
// affect other adapters in the same run.
func TestRun_installFailureSkips(t *testing.T) {
	projects := []detect.Project{{Path: "/repo", Language: "go"}}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {
			fakeAdapter{name: "broken-install", installed: false, installErr: errors.New("no network")},
			fakeAdapter{name: "other-tool", installed: true, findings: []finding.Finding{{Tool: "other-tool"}}},
		},
	}

	results := Run(projects, adapters)

	var brokenResult, otherResult ToolResult
	for _, r := range results {
		switch r.Tool {
		case "broken-install":
			brokenResult = r
		case "other-tool":
			otherResult = r
		}
	}
	if !brokenResult.Skipped || brokenResult.Error == nil {
		t.Errorf("broken-install result = %+v, want Skipped=true with Error", brokenResult)
	}
	if otherResult.Skipped || len(otherResult.Findings) != 1 {
		t.Errorf("other-tool result = %+v, want unaffected by the sibling's install failure", otherResult)
	}
}

// Two projects sharing a language must not each independently install the
// same tool: install runs exactly once, and every waiting adapter still
// runs successfully off that single install.
func TestRun_installDedupedAcrossProjects(t *testing.T) {
	var calls int32
	projects := []detect.Project{
		{Path: "/repo1", Language: "go"},
		{Path: "/repo2", Language: "go"},
	}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {
			fakeAdapter{name: "shared-tool", installed: false, installCalls: &calls, findings: []finding.Finding{{Tool: "shared-tool"}}},
		},
	}

	results := Run(projects, adapters)

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for _, r := range results {
		if r.Skipped || len(r.Findings) != 1 {
			t.Errorf("result = %+v, want a successful run for both projects", r)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("Install called %d times across 2 projects, want 1", got)
	}
}

// The same dedup guarantee must hold on the failure path: both waiters see
// the shared install error, not just the goroutine that ran Install.
func TestRun_installFailureSharedAcrossProjects(t *testing.T) {
	var calls int32
	projects := []detect.Project{
		{Path: "/repo1", Language: "go"},
		{Path: "/repo2", Language: "go"},
	}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {
			fakeAdapter{name: "shared-broken", installed: false, installErr: errors.New("no network"), installCalls: &calls},
		},
	}

	results := Run(projects, adapters)

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for _, r := range results {
		if !r.Skipped || r.Error == nil {
			t.Errorf("result = %+v, want Skipped=true with Error for both waiters", r)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("Install called %d times across 2 projects, want 1", got)
	}
}
