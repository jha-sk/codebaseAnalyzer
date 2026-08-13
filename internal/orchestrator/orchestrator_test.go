package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
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
	if panicResult.Path != "/repo" {
		t.Errorf("panicky-tool Path = %q, want %q", panicResult.Path, "/repo")
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

// ToolResult must carry the project path it ran against, so two instances
// of the same tool across multiple detected projects (e.g. two skipped
// govulncheck notes) can be told apart instead of printing identical lines.
func TestRun_toolResultCarriesProjectPath(t *testing.T) {
	projects := []detect.Project{
		{Path: "/repo1", Language: "go"},
		{Path: "/repo2", Language: "go"},
	}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "sometool", installed: true, findings: []finding.Finding{{Tool: "sometool"}}}},
	}

	results := Run(projects, adapters)

	paths := map[string]bool{}
	for _, r := range results {
		paths[r.Path] = true
	}
	if !paths["/repo1"] || !paths["/repo2"] {
		t.Fatalf("results = %+v, want one result carrying Path=/repo1 and one carrying Path=/repo2", results)
	}
}

// An absolute finding.File must come back relative to the project root it
// was found under, so adapters that report absolute paths (gosec) and
// adapters that report already-relative paths (golangci-lint) render the
// same file identically instead of as two different-looking entries.
func TestRun_normalizesAbsoluteFilePaths(t *testing.T) {
	projects := []detect.Project{{Path: "/repo", Language: "go"}}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {
			fakeAdapter{name: "abs-tool", installed: true, findings: []finding.Finding{
				{Tool: "abs-tool", File: "/repo/sub/file.go"},
			}},
			fakeAdapter{name: "rel-tool", installed: true, findings: []finding.Finding{
				{Tool: "rel-tool", File: "sub/file.go"},
			}},
			fakeAdapter{name: "no-file-tool", installed: true, findings: []finding.Finding{
				{Tool: "no-file-tool", File: ""},
			}},
		},
	}

	results := Run(projects, adapters)

	for _, r := range results {
		switch r.Tool {
		case "abs-tool":
			if got := r.Findings[0].File; got != "sub/file.go" {
				t.Errorf("abs-tool File = %q, want %q (relative to project root)", got, "sub/file.go")
			}
		case "rel-tool":
			if got := r.Findings[0].File; got != "sub/file.go" {
				t.Errorf("rel-tool File = %q, want unchanged %q", got, "sub/file.go")
			}
		case "no-file-tool":
			if got := r.Findings[0].File; got != "" {
				t.Errorf("no-file-tool File = %q, want empty (left untouched)", got)
			}
		}
	}
}

// The project root passed to Run can itself be relative (e.g. `analyser run
// testdata/fixtures/go-repo` from the repo root), while an adapter's
// absolute File is always fully absolute (gosec resolves ./... against its
// own working directory). filepath.Rel errors if exactly one of its two
// arguments is absolute, so this pins that normalizeFilePaths resolves a
// relative root to absolute before computing Rel, rather than silently
// leaving the path untouched.
func TestRun_normalizesAbsoluteFilePaths_relativeProjectRoot(t *testing.T) {
	root := t.TempDir()
	relRoot, err := filepath.Rel(mustGetwd(t), root)
	if err != nil {
		t.Skipf("project root %q isn't reachable relative to the working directory: %v", root, err)
	}

	projects := []detect.Project{{Path: relRoot, Language: "go"}}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "abs-tool", installed: true, findings: []finding.Finding{
			{Tool: "abs-tool", File: filepath.Join(root, "sub", "file.go")},
		}}},
	}

	results := Run(projects, adapters)

	if len(results) != 1 || len(results[0].Findings) != 1 {
		t.Fatalf("got %+v, want one result with one finding", results)
	}
	if got := results[0].Findings[0].File; got != filepath.Join("sub", "file.go") {
		t.Errorf("File = %q, want %q (relative to the relative project root)", got, filepath.Join("sub", "file.go"))
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
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

// An absolute path that lies outside the project root should remain absolute
// rather than becoming a ../../../.. traversal chain, which is less readable
// and less useful for tools processing the output.
func TestRun_keepsAbsolutePathsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outsideFile := filepath.Join(filepath.Dir(root), "outside.go")

	projects := []detect.Project{{Path: root, Language: "go"}}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "outside-tool", installed: true, findings: []finding.Finding{
			{Tool: "outside-tool", File: outsideFile},
		}}},
	}

	results := Run(projects, adapters)

	if len(results) != 1 || len(results[0].Findings) != 1 {
		t.Fatalf("got %+v, want one result with one finding", results)
	}
	if got := results[0].Findings[0].File; got != outsideFile {
		t.Errorf("File = %q, want %q (absolute path outside root, not traversal chain)", got, outsideFile)
	}
}

// A filename that legitimately begins with ".." should not be misclassified
// as escaping the root. The detector checks for ".." as a path component,
// not just a string prefix.
func TestRun_allowsDoubleDotFileNames(t *testing.T) {
	root := t.TempDir()
	weirdName := "..hidden.go"
	weirdFile := filepath.Join(root, weirdName)
	if err := os.WriteFile(weirdFile, []byte("// test"), 0o644); err != nil {
		t.Skipf("could not create test file: %v", err)
	}

	projects := []detect.Project{{Path: root, Language: "go"}}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "weird-tool", installed: true, findings: []finding.Finding{
			{Tool: "weird-tool", File: weirdFile},
		}}},
	}

	results := Run(projects, adapters)

	if len(results) != 1 || len(results[0].Findings) != 1 {
		t.Fatalf("got %+v, want one result with one finding", results)
	}
	if got := results[0].Findings[0].File; got != weirdName {
		t.Errorf("File = %q, want %q (relative to root, not misclassified as escaping)", got, weirdName)
	}
}

// Absolute paths inside the root must still become relative even after
// the escape-detection fix. This ensures the fix doesn't regress the
// core normalization behavior.
func TestRun_normalizesInsideRootAfterEscapeCheck(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "sub", "file.go")

	projects := []detect.Project{{Path: root, Language: "go"}}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "abs-tool", installed: true, findings: []finding.Finding{
			{Tool: "abs-tool", File: file},
		}}},
	}

	results := Run(projects, adapters)

	if len(results) != 1 || len(results[0].Findings) != 1 {
		t.Fatalf("got %+v, want one result with one finding", results)
	}
	if got := results[0].Findings[0].File; got != filepath.Join("sub", "file.go") {
		t.Errorf("File = %q, want %q (relative to root)", got, filepath.Join("sub", "file.go"))
	}
}
