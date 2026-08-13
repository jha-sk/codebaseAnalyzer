package adapter

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"

	"codebase-analyser/internal/finding"
)

type CargoAudit struct{}

func (CargoAudit) Name() string         { return "cargo-audit" }
func (CargoAudit) CheckInstalled() bool { return commandExists("cargo-audit") }

func (CargoAudit) Install() error {
	return exec.Command("cargo", "install", "cargo-audit").Run()
}

// cargoAuditEntry is the shape shared by both vulnerabilities.list[] entries
// and warnings.<kind>[] entries: only the fields the adapter actually
// surfaces. Real cargo-audit output carries much more (versions, affected,
// cvss, description, ...) which is intentionally left unparsed.
type cargoAuditEntry struct {
	Advisory struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"advisory"`
	Package struct {
		Name string `json:"name"`
	} `json:"package"`
}

type cargoAuditOutput struct {
	Vulnerabilities struct {
		List []cargoAuditEntry `json:"list"`
	} `json:"vulnerabilities"`
	// Warnings holds informational advisories (unmaintained/unsound/notice),
	// keyed by kind. Real cargo-audit output never puts these in
	// vulnerabilities.list - they live here instead.
	Warnings map[string][]cargoAuditEntry `json:"warnings"`
}

// cargoAuditSeverity maps a warnings.<kind> map key to a normalized
// severity. Keyed off the map key (i.e. cargo-audit's own grouping) rather
// than each entry's advisory.informational field: the map key is guaranteed
// present by construction (it's the JSON object key cargo-audit grouped the
// entry under), whereas advisory.informational is a nested, reused field
// that is also present - as null - on real vulnerabilities. That overlap is
// exactly what caused the original bug (severity keyed off a field that's
// always null for real vulnerabilities). If cargo-audit ever emitted an
// entry whose advisory.informational disagreed with or omitted the kind, the
// map key would still be correct.
var cargoAuditSeverity = map[string]finding.Severity{
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
	total := len(parsed.Vulnerabilities.List)
	for _, list := range parsed.Warnings {
		total += len(list)
	}
	findings := make([]finding.Finding, 0, total)

	for _, v := range parsed.Vulnerabilities.List {
		// ponytail: severity is a flat "high" for every real vulnerability,
		// not derived from advisory.cvss (present in real output as a vector
		// string, e.g. "CVSS:3.1/AV:L/AC:L/...", not a numeric score - honest
		// severity grading needs base-score computation from that vector).
		// Ceiling: two vulnerabilities of very different real-world severity
		// both land on "high". Upgrade path: parse the CVSS vector and
		// compute the base score if gating on that granularity proves
		// necessary.
		findings = append(findings, cargoAuditFinding(v, finding.SeverityHigh))
	}

	// Sort kinds for deterministic output; map iteration order is random.
	kinds := make([]string, 0, len(parsed.Warnings))
	for kind := range parsed.Warnings {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		severity, ok := cargoAuditSeverity[kind]
		if !ok {
			severity = finding.SeverityMedium
		}
		for _, w := range parsed.Warnings[kind] {
			findings = append(findings, cargoAuditFinding(w, severity))
		}
	}
	return findings
}

func cargoAuditFinding(e cargoAuditEntry, severity finding.Severity) finding.Finding {
	return finding.Finding{
		File:     "Cargo.lock",
		Line:     0,
		Tool:     "cargo-audit",
		RuleID:   e.Advisory.ID,
		Category: finding.CategorySecurity,
		Severity: severity,
		Message:  fmt.Sprintf("%s (%s)", e.Advisory.Title, e.Package.Name),
	}
}
