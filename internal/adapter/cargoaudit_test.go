package adapter

import (
	"encoding/json"
	"os"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestCargoAudit_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/cargoaudit_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed cargoAuditOutput
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	findings := cargoAuditFindings(parsed)

	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	f := findings[0]
	if f.RuleID != "RUSTSEC-2021-0001" || f.File != "Cargo.lock" {
		t.Errorf("finding = %+v", f)
	}
	if f.Category != finding.CategorySecurity || f.Severity != finding.SeverityHigh {
		t.Errorf("category/severity = %v/%v", f.Category, f.Severity)
	}
}
