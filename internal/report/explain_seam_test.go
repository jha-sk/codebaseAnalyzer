package report

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"codebase-analyser/internal/explain"
	"codebase-analyser/internal/finding"
)

// stubExplainer is a minimal explain.Explainer that returns a fixed
// Explanation/FixPattern pair, so this test exercises the real
// explain.Group -> report.RenderHuman/RenderJSON path end to end.
type stubExplainer struct{}

func (stubExplainer) Explain(ctx context.Context, tool, ruleID, sampleMessage string, count int) (explain.Explanation, error) {
	return explain.Explanation{Text: "why it matters", FixPattern: "the general fix pattern"}, nil
}

// TestExplainToReportSeam_FixPatternReachesBothRenderers is Critical 3's
// seam test: FixPattern was computed by explain.Group and attached to every
// ExplainedFinding, but no test exercised the path from there into a
// renderer, which is exactly why no test caught it never being printed.
func TestExplainToReportSeam_FixPatternReachesBothRenderers(t *testing.T) {
	findings := []finding.Finding{
		{File: "bad.go", Line: 6, Tool: "gosec", RuleID: "G101",
			Category: finding.CategorySecurity, Severity: finding.SeverityCritical, Message: "hardcoded credential"},
	}
	explained := explain.Group(context.Background(), stubExplainer{}, findings)

	var human bytes.Buffer
	RenderHuman(&human, explained, nil)
	if !strings.Contains(human.String(), "the general fix pattern") {
		t.Errorf("fix pattern from explain.Group did not reach RenderHuman output:\n%s", human.String())
	}

	var jsonBuf bytes.Buffer
	if err := RenderJSON(&jsonBuf, explained, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonBuf.String(), "the general fix pattern") {
		t.Errorf("fix pattern from explain.Group did not reach RenderJSON output:\n%s", jsonBuf.String())
	}
}
