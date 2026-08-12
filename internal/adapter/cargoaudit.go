package adapter

import (
	"encoding/json"
	"fmt"
	"os/exec"

	"codebase-analyser/internal/finding"
)

type CargoAudit struct{}

func (CargoAudit) Name() string         { return "cargo-audit" }
func (CargoAudit) CheckInstalled() bool { return commandExists("cargo-audit") }

func (CargoAudit) Install() error {
	return exec.Command("cargo", "install", "cargo-audit").Run()
}

type cargoAuditOutput struct {
	Vulnerabilities struct {
		List []struct {
			Advisory struct {
				ID            string `json:"id"`
				Title         string `json:"title"`
				Informational string `json:"informational"`
			} `json:"advisory"`
			Package struct {
				Name string `json:"name"`
			} `json:"package"`
		} `json:"list"`
	} `json:"vulnerabilities"`
}

// cargoAuditSeverity maps RustSec's advisory.informational field to a
// normalized severity. Empty/absent means an actual vulnerability (not an
// informational notice), which maps to high.
var cargoAuditSeverity = map[string]finding.Severity{
	"":             finding.SeverityHigh,
	"unmaintained": finding.SeverityLow,
	"unsound":      finding.SeverityMedium,
	"notice":       finding.SeverityLow,
}

func (CargoAudit) Run(path string) ([]finding.Finding, error) {
	out, err := runCommand(path, "cargo", "audit", "--json")
	if err != nil {
		return nil, fmt.Errorf("cargo-audit: %w", err)
	}
	var parsed cargoAuditOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("cargo-audit: parsing output: %w", err)
	}
	return cargoAuditFindings(parsed), nil
}

func cargoAuditFindings(parsed cargoAuditOutput) []finding.Finding {
	findings := make([]finding.Finding, 0, len(parsed.Vulnerabilities.List))
	for _, v := range parsed.Vulnerabilities.List {
		// ponytail: severity derives only from advisory.informational, not the
		// CVSS vector string (RustSec's cvss field is "CVSS:3.1/AV:N/...", not
		// a score — honest support means implementing base-score computation).
		// Ceiling: two real vulnerabilities differing by CVSS score still both
		// land on "high". Upgrade path: parse cvss and compute the base score
		// if gating on that granularity proves necessary.
		severity, ok := cargoAuditSeverity[v.Advisory.Informational]
		if !ok {
			severity = finding.SeverityMedium
		}
		findings = append(findings, finding.Finding{
			File:     "Cargo.lock",
			Line:     0,
			Tool:     "cargo-audit",
			RuleID:   v.Advisory.ID,
			Category: finding.CategorySecurity,
			Severity: severity,
			Message:  fmt.Sprintf("%s (%s)", v.Advisory.Title, v.Package.Name),
		})
	}
	return findings
}
