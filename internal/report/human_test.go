package report

import (
	"bytes"
	"strings"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestRenderHuman(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{File: "bad.go", Line: 6, Tool: "gosec", RuleID: "G101",
			Category: finding.CategorySecurity, Severity: finding.SeverityCritical, Message: "hardcoded credential"},
			Explanation: "secrets in source get leaked via git history"},
	}
	var buf bytes.Buffer
	RenderHuman(&buf, findings, nil)
	out := buf.String()

	if !strings.Contains(out, "critical=1") {
		t.Errorf("summary missing critical=1: %s", out)
	}
	if !strings.Contains(out, "security") {
		t.Errorf("missing category header: %s", out)
	}
	if !strings.Contains(out, "bad.go:6") {
		t.Errorf("missing file:line: %s", out)
	}
	if !strings.Contains(out, "secrets in source get leaked") {
		t.Errorf("missing explanation: %s", out)
	}
}

func TestSummary(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{Severity: finding.SeverityHigh}},
		{Finding: finding.Finding{Severity: finding.SeverityHigh}},
		{Finding: finding.Finding{Severity: finding.SeverityLow}},
	}
	counts := Summary(findings)
	if counts[finding.SeverityHigh] != 2 || counts[finding.SeverityLow] != 1 {
		t.Errorf("got %+v", counts)
	}
}

// TestRenderHumanEmpty ensures an empty findings slice still produces a
// sensible report (a zeroed summary line, no panics, no category headers).
func TestRenderHumanEmpty(t *testing.T) {
	var buf bytes.Buffer
	RenderHuman(&buf, nil, nil)
	out := buf.String()

	if !strings.Contains(out, "critical=0 high=0 medium=0 low=0") {
		t.Errorf("expected zeroed summary line, got: %s", out)
	}
	if strings.Contains(out, "==") {
		t.Errorf("expected no category headers for empty input, got: %s", out)
	}
}

// TestRenderHumanDeterministicOrder locks in category/severity/rule ordering
// across many runs, guarding against Go's randomized map iteration leaking
// into output order. The fixture is built so every grouping level has
// something to shuffle: two severities inside one category (severity order),
// and three rules inside one (category, severity) group whose first
// appearances interleave — rule A, rule B, rule A again, rule C (rule
// first-appearance order) — plus four distinct categories (category order).
// A regression to unordered map iteration at any of the three levels would
// make this test fail, either by producing non-identical output across
// repeated runs or by producing a consistent output in the wrong order.
func TestRenderHumanDeterministicOrder(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{File: "a1.go", Line: 1, Tool: "toolA", RuleID: "R1",
			Category: finding.CategoryCorrectness, Severity: finding.SeverityHigh, Message: "c-h-1"}},
		{Finding: finding.Finding{File: "o1.go", Line: 1, Tool: "toolH", RuleID: "R8",
			Category: finding.CategoryOperational, Severity: finding.SeverityHigh, Message: "o-h-1"}},
		{Finding: finding.Finding{File: "d1.go", Line: 1, Tool: "toolD", RuleID: "R4",
			Category: finding.CategoryCorrectness, Severity: finding.SeverityLow, Message: "c-l-1"}},
		{Finding: finding.Finding{File: "b1.go", Line: 1, Tool: "toolB", RuleID: "R2",
			Category: finding.CategoryCorrectness, Severity: finding.SeverityHigh, Message: "c-h-2"}},
		{Finding: finding.Finding{File: "m1.go", Line: 1, Tool: "toolM", RuleID: "R5",
			Category: finding.CategoryConcurrency, Severity: finding.SeverityMedium, Message: "cc-m-1"}},
		{Finding: finding.Finding{File: "a2.go", Line: 2, Tool: "toolA", RuleID: "R1",
			Category: finding.CategoryCorrectness, Severity: finding.SeverityHigh, Message: "c-h-3"}},
		{Finding: finding.Finding{File: "s1.go", Line: 1, Tool: "toolS", RuleID: "R6",
			Category: finding.CategorySecurity, Severity: finding.SeverityCritical, Message: "s-c-1"}},
		{Finding: finding.Finding{File: "c1.go", Line: 1, Tool: "toolC", RuleID: "R3",
			Category: finding.CategoryCorrectness, Severity: finding.SeverityHigh, Message: "c-h-4"}},
	}

	var first string
	for i := 0; i < 20; i++ {
		var buf bytes.Buffer
		RenderHuman(&buf, findings, nil)
		out := buf.String()
		if i == 0 {
			first = out
			continue
		}
		if out != first {
			t.Fatalf("output not deterministic across runs (iteration %d):\n--- first ---\n%s\n--- got ---\n%s", i, first, out)
		}
	}

	// Category order must follow categoryOrder: correctness, concurrency, security, operational.
	wantCategories := []string{"correctness", "concurrency", "security", "operational"}
	lastIdx := -1
	for _, cat := range wantCategories {
		idx := strings.Index(first, "== "+cat+" ==")
		if idx == -1 {
			t.Fatalf("missing category header for %s:\n%s", cat, first)
		}
		if idx < lastIdx {
			t.Fatalf("category %s appears out of order:\n%s", cat, first)
		}
		lastIdx = idx
	}

	// Severity order within the correctness category: high before low.
	idxHigh := strings.Index(first, "[high]")
	idxLow := strings.Index(first, "[low]")
	if idxHigh == -1 || idxLow == -1 {
		t.Fatalf("missing severity headers:\n%s", first)
	}
	if idxHigh > idxLow {
		t.Fatalf("severity order wrong: [high] should appear before [low]:\n%s", first)
	}

	// Rule first-appearance order within correctness/high: R1, R2, R3 (R1
	// reappears but keeps its original first-appearance slot).
	wantRules := []string{"toolA/R1", "toolB/R2", "toolC/R3"}
	lastIdx = -1
	for _, rule := range wantRules {
		idx := strings.Index(first, "[high] "+rule)
		if idx == -1 {
			t.Fatalf("missing rule header for %s:\n%s", rule, first)
		}
		if idx < lastIdx {
			t.Fatalf("rule %s appears out of order:\n%s", rule, first)
		}
		lastIdx = idx
	}
}

// TestRenderHumanEmptyExplanation ensures a finding whose Explanation is the
// empty string (the LLM-failure fallback) still renders cleanly: its rule
// header is followed directly by its file:line entry, with no stray blank
// explanation line in between. Mixed against a sibling rule that does have
// an explanation, so the no-explanation case is the one under test rather
// than the only case in play.
func TestRenderHumanEmptyExplanation(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{File: "x.go", Line: 1, Tool: "t", RuleID: "R",
			Category: finding.CategoryCorrectness, Severity: finding.SeverityLow, Message: "msg"},
			Explanation: ""},
		{Finding: finding.Finding{File: "y.go", Line: 2, Tool: "u", RuleID: "S",
			Category: finding.CategoryCorrectness, Severity: finding.SeverityLow, Message: "msg2"},
			Explanation: "has an explanation"},
	}
	var buf bytes.Buffer
	RenderHuman(&buf, findings, nil)
	out := buf.String()

	noExplanationBlock := "  [low] t/R (1)\n    - x.go:1 msg\n"
	if !strings.Contains(out, noExplanationBlock) {
		t.Errorf("expected rule header immediately followed by file:line with no gap, got:\n%s", out)
	}

	withExplanationBlock := "  [low] u/S (1)\n    has an explanation\n    - y.go:2 msg2\n"
	if !strings.Contains(out, withExplanationBlock) {
		t.Errorf("expected explanation line between header and file:line for the other rule, got:\n%s", out)
	}
}

// TestRenderHumanUnknownCategoryAndSeverityNotDropped ensures findings whose
// Category or Severity falls outside the known enum still appear in the
// report body (under a trailing "other" bucket) rather than silently
// vanishing while still being counted in the summary line.
func TestRenderHumanUnknownCategoryAndSeverityNotDropped(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{File: "bogus-cat.go", Line: 1, Tool: "t", RuleID: "R1",
			Category: finding.Category("made-up-category"), Severity: finding.SeverityHigh, Message: "orphan category"}},
		{Finding: finding.Finding{File: "bogus-sev.go", Line: 2, Tool: "t", RuleID: "R2",
			Category: finding.CategoryCorrectness, Severity: finding.Severity("urgent"), Message: "orphan severity"}},
	}
	var buf bytes.Buffer
	RenderHuman(&buf, findings, nil)
	out := buf.String()

	if !strings.Contains(out, "critical=0 high=1 medium=0 low=0") {
		// Summary only tallies the four known severities; the bogus one
		// ("urgent") isn't among them, so it doesn't add to any bucket.
		t.Errorf("unexpected summary line: %s", out)
	}
	if !strings.Contains(out, "== other ==") {
		t.Errorf("expected a trailing 'other' category bucket, got:\n%s", out)
	}
	if !strings.Contains(out, "bogus-cat.go:1 orphan category") {
		t.Errorf("finding with unrecognized category was dropped:\n%s", out)
	}
	if !strings.Contains(out, "bogus-sev.go:2 orphan severity") {
		t.Errorf("finding with unrecognized severity was dropped:\n%s", out)
	}
	if !strings.Contains(out, "[urgent]") {
		t.Errorf("expected the unrecognized severity label to appear, got:\n%s", out)
	}
}

// TestRenderHumanFixPattern is Critical 3's regression test: FixPattern was
// computed by the LLM step and attached to every ExplainedFinding, but no
// renderer ever printed it - the "do" half of the spec's "do's and don'ts"
// framing never reached the user. It must render alongside the explanation,
// and must not leave a stray label/blank line when empty (the --no-llm or
// failed-group case).
func TestRenderHumanFixPattern(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{File: "x.go", Line: 1, Tool: "t", RuleID: "R",
			Category: finding.CategoryCorrectness, Severity: finding.SeverityLow, Message: "msg"},
			Explanation: "why it matters", FixPattern: "use context.WithTimeout instead"},
		{Finding: finding.Finding{File: "y.go", Line: 2, Tool: "u", RuleID: "S",
			Category: finding.CategoryCorrectness, Severity: finding.SeverityLow, Message: "msg2"},
			Explanation: "", FixPattern: ""},
	}
	var buf bytes.Buffer
	RenderHuman(&buf, findings, nil)
	out := buf.String()

	withFixBlock := "  [low] t/R (1)\n    why it matters\n    Fix: use context.WithTimeout instead\n    - x.go:1 msg\n"
	if !strings.Contains(out, withFixBlock) {
		t.Errorf("expected fix pattern rendered under the explanation, got:\n%s", out)
	}

	noFixBlock := "  [low] u/S (1)\n    - y.go:2 msg2\n"
	if !strings.Contains(out, noFixBlock) {
		t.Errorf("expected no stray Fix line when FixPattern is empty, got:\n%s", out)
	}
	if strings.Contains(out, "Fix: \n") {
		t.Errorf("expected no blank Fix line, got:\n%s", out)
	}
}

// TestRenderHumanSkippedToolsCoverageIncomplete is Critical 2's report-side
// regression test: incomplete coverage (some tool skipped) must be visible
// in the report body itself, not just in the exit code or an external note.
func TestRenderHumanSkippedToolsCoverageIncomplete(t *testing.T) {
	var buf bytes.Buffer
	RenderHuman(&buf, nil, []SkippedTool{
		{Tool: "govulncheck", Path: "/repo", Reason: "govulncheck: not found in $PATH"},
	})
	out := buf.String()

	if !strings.Contains(out, "COVERAGE INCOMPLETE") {
		t.Errorf("expected an explicit incomplete-coverage marker, got:\n%s", out)
	}
	if !strings.Contains(out, "govulncheck") || !strings.Contains(out, "/repo") {
		t.Errorf("expected the skipped tool's name and path in the report, got:\n%s", out)
	}
}

// TestRenderHumanNoSkippedToolsNoCoverageMarker ensures the clean-run path
// (nothing skipped) doesn't grow a spurious incomplete-coverage line.
func TestRenderHumanNoSkippedToolsNoCoverageMarker(t *testing.T) {
	var buf bytes.Buffer
	RenderHuman(&buf, nil, nil)
	out := buf.String()
	if strings.Contains(out, "COVERAGE INCOMPLETE") {
		t.Errorf("expected no incomplete-coverage marker when nothing was skipped, got:\n%s", out)
	}
}
