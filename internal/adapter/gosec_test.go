package adapter

import (
	"encoding/json"
	"os"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestGosec_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/gosec_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed gosecOutput
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	findings := gosecFindings(parsed)

	if len(findings) != 4 {
		t.Fatalf("got %d findings, want 4", len(findings))
	}
	if findings[0].Severity != finding.SeverityCritical || findings[0].Category != finding.CategorySecurity {
		t.Errorf("finding[0] = %+v", findings[0])
	}
	if findings[1].Line != 8 {
		t.Errorf("finding[1].Line = %d, want 8 (parsed from %q)", findings[1].Line, "8-9")
	}
	if findings[1].Severity != finding.SeverityMedium {
		t.Errorf("finding[1].Severity = %v, want medium", findings[1].Severity)
	}
	if findings[2].Severity != finding.SeverityMedium {
		t.Errorf("finding[2].Severity = %v, want medium (unrecognized severity %q falls back)", findings[2].Severity, "CRITICAL")
	}
	// HIGH severity/LOW confidence is gosec's classic false-positive shape
	// (e.g. G101's entropy heuristic). It must be damped one level below
	// what a HIGH/HIGH finding of the same rule would get, not report
	// identically as "critical".
	if findings[3].Severity != finding.SeverityHigh {
		t.Errorf("finding[3].Severity = %v, want high (HIGH severity damped one level for LOW confidence)", findings[3].Severity)
	}
}
