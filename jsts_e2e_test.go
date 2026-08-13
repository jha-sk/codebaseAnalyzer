package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codebase-analyser/internal/cli"
	"codebase-analyser/internal/finding"
)

// requireJSTooling gates the JS/TS end-to-end tests. They are the only
// tests in this package that need real Node tooling: npm itself, plus the
// pinned eslint/typescript/eslint-plugin-security install that
// DefaultAdapters shells out to for "js"/"ts" projects (see
// adapter.installJSTools). Also gated behind testing.Short() so
// `go test -short ./...` stays fast and fully offline - run the full
// (non -short) form, e.g. in CI, to exercise this coverage for real.
func requireJSTooling(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("short mode: skipping test that needs real Node tooling")
	}
	requireTool(t, "npm")
}

// TestEndToEnd_JS_findsKnownIssues covers the JS fixture, guarded only by
// the tools DefaultAdapters runs for a "js" project (ESLint and js-audit,
// via npm). testdata/fixtures/js-repo carries a real package-lock.json
// pinning minimist@0.0.8 (a known-vulnerable version) specifically so
// js-audit has a lockfile to run end-to-end here - see Important 7: no
// fixture had a lockfile before, so a broken js-audit invocation could ship
// unnoticed, which is exactly how npm-audit-reads-network-failure-as-clean
// (Critical 1) got past review the first time.
func TestEndToEnd_JS_findsKnownIssues(t *testing.T) {
	requireJSTooling(t)

	ruleIDs, skippedTools := runE2EWithSkipped(t, "testdata/fixtures/js-repo")
	joined := strings.Join(ruleIDs, ",")
	// The baseline config enables eslint-plugin-security's
	// detect-eval-with-expression rule, not core no-eval - no-eval isn't
	// part of eslint:recommended, so the baseline never turns it on. That
	// rule stays in the classification table for repos whose own ESLint
	// config enables it directly. detect-eval-with-expression is what
	// actually fires on eval(userInput) here, and it's already mapped to
	// (security, critical).
	if !strings.Contains(joined, "eslint:security/detect-eval-with-expression") {
		t.Errorf("expected eslint:security/detect-eval-with-expression (security) in findings, got %s", joined)
	}
	if !strings.Contains(joined, "eslint:no-undef") {
		t.Errorf("expected eslint:no-undef (undeclared variable) in findings, got %s", joined)
	}
	if !strings.Contains(joined, "eslint:promise/catch-or-return") {
		t.Errorf("expected eslint:promise/catch-or-return (floating promise) in findings, got %s", joined)
	}

	// Deliberately NOT asserting that js-audit produced any specific finding
	// (or even a nonzero count): that would depend on minimist@0.0.8 still
	// being flagged by whatever advisory database npm audit consults, which
	// is data outside this repo's control and will rot the test for a
	// reason unrelated to the code under test. What actually matters, and
	// what does not rot, is that js-audit was invoked at all - a tool
	// invoked with a wrong flag that silently returns zero findings forever
	// is the failure mode this repo has shipped twice, and the
	// not-in-skipped-tools check below is what catches that.
	for _, tool := range skippedTools {
		if tool == "js-audit" {
			t.Errorf("js-audit is in the skipped-tools list, want it to have run: %v", skippedTools)
		}
	}
}

// TestEndToEnd_TS_findsKnownIssues covers the TS fixture, guarded only by
// the tools DefaultAdapters runs for a "ts" project (ESLint + tsc, via npm).
func TestEndToEnd_TS_findsKnownIssues(t *testing.T) {
	requireJSTooling(t)

	ruleIDs := runE2E(t, "testdata/fixtures/ts-repo")
	joined := strings.Join(ruleIDs, ",")
	var sawTsc bool
	for _, r := range ruleIDs {
		if strings.HasPrefix(r, "tsc:") {
			sawTsc = true
			break
		}
	}
	if !sawTsc {
		t.Errorf("expected a tsc:* (correctness) finding for the string-to-number type error, got %s", joined)
	}
}

// runE2EWithSkipped runs the real pipeline against path, like runE2E, but
// also returns the names of tools the orchestrator reported as skipped -
// runE2E/runE2EFindings only expose findings, which would leave a tool that
// ran but happened to find nothing indistinguishable from one that never
// ran at all.
func runE2EWithSkipped(t *testing.T, path string) (ruleIDs []string, skippedTools []string) {
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
		Findings     []e2eFinding `json:"findings"`
		SkippedTools []struct {
			Tool string `json:"tool"`
		} `json:"skippedTools"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, buf.String())
	}
	for _, f := range parsed.Findings {
		ruleIDs = append(ruleIDs, f.Tool+":"+f.RuleID)
	}
	for _, s := range parsed.SkippedTools {
		skippedTools = append(skippedTools, s.Tool)
	}
	return ruleIDs, skippedTools
}
