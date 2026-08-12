package adapter

import (
	"encoding/json"
	"os"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestGolangciLint_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/golangci_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed golangciOutput
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	findings := golangciFindings(parsed)

	if len(findings) != 5 {
		t.Fatalf("got %d findings, want 5", len(findings))
	}

	f0 := findings[0]
	if f0.File != "bad.go" || f0.Line != 8 || f0.Tool != "golangci-lint" || f0.RuleID != "errcheck" {
		t.Errorf("finding[0] = %+v", f0)
	}
	if f0.Category != finding.CategoryCorrectness || f0.Severity != finding.SeverityHigh {
		t.Errorf("finding[0] category/severity = %v/%v", f0.Category, f0.Severity)
	}

	f1 := findings[1]
	if f1.RuleID != "SA2001" || f1.Category != finding.CategoryConcurrency {
		t.Errorf("finding[1] = %+v, want RuleID=SA2001 category=concurrency", f1)
	}

	// govet copylocks messages have no analyzer-name field in golangci-lint's
	// JSON, so they're routed to concurrency by matching on message text.
	f2 := findings[2]
	if f2.RuleID != "govet" || f2.Category != finding.CategoryConcurrency || f2.Severity != finding.SeverityHigh {
		t.Errorf("finding[2] = %+v, want RuleID=govet category=concurrency severity=high", f2)
	}

	// bodyclose is an operational linter (production-readiness value-add).
	f3 := findings[3]
	if f3.RuleID != "bodyclose" || f3.Category != finding.CategoryOperational || f3.Severity != finding.SeverityMedium {
		t.Errorf("finding[3] = %+v, want RuleID=bodyclose category=operational severity=medium", f3)
	}

	// gocritic isn't in linterCategory/linterSeverity, so it must fall back
	// to correctness/medium, and pickRuleID must fall back to the linter name
	// since the message carries no SA-prefixed rule code.
	f4 := findings[4]
	if f4.RuleID != "gocritic" || f4.Category != finding.CategoryCorrectness || f4.Severity != finding.SeverityMedium {
		t.Errorf("finding[4] = %+v, want RuleID=gocritic category=correctness severity=medium (fallback)", f4)
	}
}
