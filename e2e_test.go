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

// runE2E runs the real pipeline against path and returns the tool:ruleID
// pairs found, so each test's assertion reads as one line.
func runE2E(t *testing.T, path string) []string {
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
		Findings []struct {
			RuleID string `json:"ruleID"`
			Tool   string `json:"tool"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, buf.String())
	}

	var ruleIDs []string
	for _, f := range parsed.Findings {
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

	ruleIDs := runE2E(t, "testdata/fixtures/go-repo")
	joined := strings.Join(ruleIDs, ",")
	if !strings.Contains(joined, "gosec:G101") {
		t.Errorf("expected gosec:G101 (hardcoded credential) in findings, got %s", joined)
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
