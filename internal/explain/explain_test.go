package explain

import (
	"context"
	"errors"
	"testing"

	"codebase-analyser/internal/finding"
)

type stubExplainer struct {
	calls int
	fail  map[string]bool // ruleID -> should fail
}

func (s *stubExplainer) Explain(ctx context.Context, tool, ruleID, sampleMessage string, count int) (Explanation, error) {
	s.calls++
	if s.fail[ruleID] {
		return Explanation{}, errors.New("llm down")
	}
	return Explanation{Text: "why: " + ruleID, FixPattern: "fix: " + ruleID}, nil
}

func TestGroup_batchesPerToolAndRule(t *testing.T) {
	findings := []finding.Finding{
		{Tool: "gosec", RuleID: "G101", Message: "m1"},
		{Tool: "gosec", RuleID: "G101", Message: "m2"},
		{Tool: "gosec", RuleID: "G104", Message: "m3"},
	}
	stub := &stubExplainer{fail: map[string]bool{}}

	out := Group(context.Background(), stub, findings)

	if stub.calls != 2 {
		t.Fatalf("got %d Explain calls, want 2 (one per rule)", stub.calls)
	}
	if len(out) != 3 {
		t.Fatalf("got %d explained findings, want 3", len(out))
	}
	for _, f := range out {
		if f.Explanation == "" {
			t.Errorf("finding %+v missing explanation", f)
		}
	}
}

func TestGroup_llmFailureFallsBackToUnexplained(t *testing.T) {
	findings := []finding.Finding{{Tool: "gosec", RuleID: "G101", Message: "m1"}}
	stub := &stubExplainer{fail: map[string]bool{"G101": true}}

	out := Group(context.Background(), stub, findings)

	if len(out) != 1 || out[0].Explanation != "" {
		t.Fatalf("got %+v, want one finding with no explanation", out)
	}
}
