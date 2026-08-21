// python_e2e_test.go
package e2e

import (
	"strings"
	"testing"
)

// requirePythonTooling gates the Python end-to-end test on real tooling:
// python3 itself, plus the pinned ruff/mypy/pip-audit venv DefaultAdapters
// shells out to for "python" projects (see adapter.installPyTools). Also
// gated behind testing.Short(), same as requireJSTooling.
func requirePythonTooling(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("short mode: skipping test that needs real Python tooling")
	}
	requireTool(t, "python3")
}

// TestEndToEnd_Python_findsKnownIssues covers the Python fixture, guarded
// only by the tools DefaultAdapters runs for a "python" project (ruff,
// mypy, pip-audit). testdata/fixtures/python-repo carries an unused import
// (ruff F401), a subprocess shell=True call (ruff S602), a mypy return-type
// mismatch, and a requirements.txt pinning an old requests version so
// pip-audit has something to run against.
func TestEndToEnd_Python_findsKnownIssues(t *testing.T) {
	requirePythonTooling(t)

	ruleIDs, skippedTools := runE2EWithSkipped(t, "testdata/fixtures/python-repo")
	joined := strings.Join(ruleIDs, ",")
	if !strings.Contains(joined, "ruff:F401") {
		t.Errorf("expected ruff:F401 (unused import) in findings, got %s", joined)
	}
	if !strings.Contains(joined, "ruff:S602") {
		t.Errorf("expected ruff:S602 (subprocess shell=True) in findings, got %s", joined)
	}
	var sawMypy bool
	for _, r := range ruleIDs {
		if strings.HasPrefix(r, "mypy:") {
			sawMypy = true
			break
		}
	}
	if !sawMypy {
		t.Errorf("expected a mypy:* (correctness) finding for the str-returned-as-int mismatch, got %s", joined)
	}

	// Deliberately NOT asserting a specific pip-audit finding (or even a
	// nonzero count): that would depend on requests==2.6.0 still being
	// flagged by whatever advisory database pip-audit consults, which is
	// data outside this repo's control - same reasoning jsts_e2e_test.go
	// already documents for its js-audit assertion. What matters is that
	// pip-audit ran at all.
	for _, tool := range skippedTools {
		if tool == "pip-audit" {
			t.Errorf("pip-audit is in the skipped-tools list, want it to have run: %v", skippedTools)
		}
	}
}
