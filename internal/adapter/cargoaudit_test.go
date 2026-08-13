package adapter

import (
	"encoding/json"
	"os"
	"testing"

	"codebase-analyser/internal/finding"
)

// testdata/cargoaudit_sample.json is derived from real `cargo audit --json`
// captures (RUSTSEC-2020-0071 in vulnerabilities.list, RUSTSEC-2020-0016 in
// warnings.unmaintained) plus a fabricated "unsound" and unrecognized-kind
// entry to exercise the rest of the severity table.
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

	// 1 real vulnerability + 3 warnings kinds (unmaintained, unsound, some-future-kind).
	if len(findings) != 4 {
		t.Fatalf("got %d findings, want 4: %+v", len(findings), findings)
	}

	byRuleID := make(map[string]finding.Finding, len(findings))
	for _, f := range findings {
		byRuleID[f.RuleID] = f
	}

	vuln, ok := byRuleID["RUSTSEC-2020-0071"]
	if !ok {
		t.Fatalf("missing RUSTSEC-2020-0071 (real vulnerability) in %+v", findings)
	}
	if vuln.Severity != finding.SeverityHigh || vuln.Category != finding.CategorySecurity || vuln.File != "Cargo.lock" {
		t.Errorf("vulnerability finding = %+v, want severity high/category security/file Cargo.lock", vuln)
	}

	unmaintained, ok := byRuleID["RUSTSEC-2020-0016"]
	if !ok {
		t.Fatalf("missing RUSTSEC-2020-0016 (warnings.unmaintained) in %+v", findings)
	}
	if unmaintained.Severity != finding.SeverityLow {
		t.Errorf("unmaintained finding severity = %v, want low", unmaintained.Severity)
	}

	unsound, ok := byRuleID["RUSTSEC-2021-0145"]
	if !ok {
		t.Fatalf("missing RUSTSEC-2021-0145 (warnings.unsound) in %+v", findings)
	}
	if unsound.Severity != finding.SeverityMedium {
		t.Errorf("unsound finding severity = %v, want medium", unsound.Severity)
	}

	unrecognized, ok := byRuleID["RUSTSEC-9999-0001"]
	if !ok {
		t.Fatalf("missing RUSTSEC-9999-0001 (warnings.some-future-kind) in %+v", findings)
	}
	if unrecognized.Severity != finding.SeverityMedium {
		t.Errorf("unrecognized-kind finding severity = %v, want medium (fallback)", unrecognized.Severity)
	}
}
