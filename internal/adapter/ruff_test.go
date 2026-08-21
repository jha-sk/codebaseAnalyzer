package adapter

import (
	"os"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestRuffFindings(t *testing.T) {
	raw, err := os.ReadFile("testdata/ruff_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := ruffFindings(raw)
	if err != nil {
		t.Fatalf("ruffFindings: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}

	var unused, shell *finding.Finding
	for i := range findings {
		switch findings[i].RuleID {
		case "F401":
			unused = &findings[i]
		case "S602":
			shell = &findings[i]
		}
	}
	if unused == nil {
		t.Fatalf("no F401 finding: %+v", findings)
	}
	if unused.Category != finding.CategoryCorrectness || unused.Severity != finding.SeverityLow {
		t.Errorf("F401 = (%v, %v), want (correctness, low)", unused.Category, unused.Severity)
	}
	if unused.Line != 1 {
		t.Errorf("F401 Line = %d, want 1", unused.Line)
	}

	if shell == nil {
		t.Fatalf("no S602 finding: %+v", findings)
	}
	if shell.Category != finding.CategorySecurity || shell.Severity != finding.SeverityHigh {
		t.Errorf("S602 = (%v, %v), want (security, high)", shell.Category, shell.Severity)
	}
	if shell.Line != 5 {
		t.Errorf("S602 Line = %d, want 5", shell.Line)
	}
}
