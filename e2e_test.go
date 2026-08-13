package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"codebase-analyser/internal/cli"
	"codebase-analyser/internal/finding"
)

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not installed, skipping e2e smoke test", name)
	}
}

type e2eFinding struct {
	Tool     string `json:"tool"`
	RuleID   string `json:"ruleID"`
	Severity string `json:"severity"`
}

// runE2EFindings runs the real pipeline against path and returns the parsed
// findings (tool, ruleID, severity), so tests can assert on severity as
// well as which rules fired.
func runE2EFindings(t *testing.T, path string) []e2eFinding {
	t.Helper()
	var buf bytes.Buffer
	_, err := cli.Execute(context.Background(), &buf, cli.RunConfig{
		Path:     path,
		Format:   "json",
		Severity: finding.SeverityLow,
		NoLLM:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Findings []e2eFinding `json:"findings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, buf.String())
	}
	return parsed.Findings
}

// runE2E runs the real pipeline against path and returns the tool:ruleID
// pairs found, so each test's assertion reads as one line.
func runE2E(t *testing.T, path string) []string {
	t.Helper()
	var ruleIDs []string
	for _, f := range runE2EFindings(t, path) {
		ruleIDs = append(ruleIDs, f.Tool+":"+f.RuleID)
	}
	return ruleIDs
}

// TestEndToEnd_Go_findsKnownIssues covers only the Go fixture, guarded only
// by the Go tools DefaultAdapters actually runs for a Go project
// (golangci-lint, gosec, govulncheck). A machine with the full Go toolchain
// but no Rust tooling (or vice versa) must still be able to run its half -
// gating this on all five tools, as an earlier version of this test did,
// means a partially-tooled machine skips checks it could otherwise run for
// real.
func TestEndToEnd_Go_findsKnownIssues(t *testing.T) {
	requireTool(t, "golangci-lint")
	requireTool(t, "gosec")
	requireTool(t, "govulncheck")

	findings := runE2EFindings(t, "testdata/fixtures/go-repo")
	var joined []string
	var g101Severity string
	for _, f := range findings {
		joined = append(joined, f.Tool+":"+f.RuleID)
		if f.Tool == "gosec" && f.RuleID == "G101" {
			g101Severity = f.Severity
		}
	}
	if g101Severity == "" {
		t.Errorf("expected gosec:G101 (hardcoded credential) in findings, got %s", strings.Join(joined, ","))
	}
	// Real gosec reports this fixture's G101 as HIGH severity/LOW confidence
	// (its entropy heuristic on a random-looking string) - the classic
	// false-positive shape. It must not report identically to a HIGH/HIGH
	// finding as "critical".
	if g101Severity == "critical" {
		t.Errorf("gosec:G101 reported as critical; want damped below critical since it's HIGH severity/LOW confidence")
	}
}

// TestEndToEnd_Rust_findsKnownIssues covers only the Rust fixture, guarded
// only by the tools DefaultAdapters runs for a Rust project (clippy via
// cargo, cargo-audit).
func TestEndToEnd_Rust_findsKnownIssues(t *testing.T) {
	requireTool(t, "cargo")
	requireTool(t, "cargo-audit")

	ruleIDs := runE2E(t, "testdata/fixtures/rust-repo")
	joined := strings.Join(ruleIDs, ",")
	if !strings.Contains(joined, "clippy:clippy::bool_comparison") {
		t.Errorf("expected clippy:clippy::bool_comparison in findings, got %s", joined)
	}
}

// TestEndToEnd_MixedRepo_detectsBothLanguages covers the spec's mixed-repo
// case: pointing Execute at a directory containing both a Go and a Rust
// project and confirming detect.Detect + the orchestrator exercise both
// language's adapters in a single run. Needs all five tools, since it's the
// only test here that actually requires the full toolchain.
func TestEndToEnd_MixedRepo_detectsBothLanguages(t *testing.T) {
	requireTool(t, "golangci-lint")
	requireTool(t, "gosec")
	requireTool(t, "govulncheck")
	requireTool(t, "cargo")
	requireTool(t, "cargo-audit")

	ruleIDs := runE2E(t, "testdata/fixtures")
	joined := strings.Join(ruleIDs, ",")
	if !strings.Contains(joined, "gosec:G101") {
		t.Errorf("expected gosec:G101 from the Go fixture in findings, got %s", joined)
	}
	if !strings.Contains(joined, "clippy:clippy::bool_comparison") {
		t.Errorf("expected clippy:clippy::bool_comparison from the Rust fixture in findings, got %s", joined)
	}
}
