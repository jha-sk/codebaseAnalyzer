package adapter

import (
	"bytes"
	"os"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestGovulncheck_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/govulncheck_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := govulncheckFindings(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	f := findings[0]
	if f.RuleID != "GO-2023-1234" || f.File != "bad.go" || f.Line != 15 {
		t.Errorf("finding = %+v", f)
	}
	if f.Category != finding.CategorySecurity || f.Severity != finding.SeverityCritical {
		t.Errorf("category/severity = %v/%v, want security/critical", f.Category, f.Severity)
	}
	if f.Message != "Denial of service via crafted input" {
		t.Errorf("message = %q", f.Message)
	}
}
