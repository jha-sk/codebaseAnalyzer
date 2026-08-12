package adapter

import (
	"bytes"
	"os"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestClippy_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/clippy_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := clippyFindings(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(findings))
	}
	if findings[0].RuleID != "clippy::bool_comparison" || findings[0].Category != finding.CategoryCorrectness {
		t.Errorf("finding[0] = %+v", findings[0])
	}
	if findings[0].Severity != finding.SeverityMedium {
		t.Errorf("finding[0].Severity = %v, want medium", findings[0].Severity)
	}
	if findings[1].RuleID != "clippy::mutex_atomic" || findings[1].Category != finding.CategoryConcurrency {
		t.Errorf("finding[1] = %+v, want concurrency category", findings[1])
	}
	if findings[1].Severity != finding.SeverityHigh {
		t.Errorf("finding[1].Severity = %v, want high", findings[1].Severity)
	}
}
