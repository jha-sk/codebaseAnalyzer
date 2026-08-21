package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"codebase-analyser/internal/finding"
)

// PipAudit scans a Python project's dependency manifest for known CVEs.
// Not Targeted: a dependency audit has no sub-unit to restrict to (one
// manifest covers the whole project) - same reasoning as CargoAudit/JSAudit.
type PipAudit struct{}

func (PipAudit) Name() string         { return "pip-audit" }
func (PipAudit) CheckInstalled() bool { return isExecutable(pinnedPyBin("pip-audit")) }
func (PipAudit) Install() error       { return installPyTools() }

// pipManifestPriority is checked in this order: requirements.txt first,
// since it's the lowest-common-denominator format pip-audit reads natively
// via -r; poetry.lock and uv.lock require pip-audit's own native lockfile
// support (no -r flag - it discovers them from the working directory).
var pipManifestPriority = []struct {
	file     string
	useDashR bool
}{
	{"requirements.txt", true},
	{"poetry.lock", false},
	{"uv.lock", false},
}

// detectPipManifest reports which supported manifest (if any) is present
// directly in dir, and whether pip-audit needs -r <file> to read it.
func detectPipManifest(dir string) (manifest string, useDashR bool) {
	for _, m := range pipManifestPriority {
		if _, err := os.Stat(filepath.Join(dir, m.file)); err == nil {
			return m.file, m.useDashR
		}
	}
	return "", false
}

func (PipAudit) Run(path string) ([]finding.Finding, error) {
	manifest, useDashR := detectPipManifest(path)
	if manifest == "" {
		// None of the 3 supported manifests. A Pipfile.lock specifically
		// means there IS a dependency set, just one pip-audit can't read in
		// v1 - report it as a named, actionable skip (spec: "reports
		// dependency-scanning as skipped, with a clear reason — not
		// silently absent"), mirroring jsaudit.go's error-vs-silence split.
		if _, err := os.Stat(filepath.Join(path, "Pipfile.lock")); err == nil {
			return nil, fmt.Errorf("pip-audit: Pipenv-only project (Pipfile.lock present, no requirements.txt/poetry.lock/uv.lock); dependency scanning is not supported for Pipenv projects in v1")
		}
		// No manifest of any kind: nothing to audit, healthy silence.
		return nil, nil
	}

	bin := pinnedPyBin("pip-audit")
	var out []byte
	var err error
	if useDashR {
		out, err = runCommand(path, bin, "-r", manifest, "--format", "json")
	} else {
		out, err = runCommand(path, bin, "--format", "json")
	}
	if err != nil {
		return nil, fmt.Errorf("pip-audit: %w", err)
	}
	findings, err := pipAuditFindings(out)
	if err != nil {
		return nil, fmt.Errorf("pip-audit: %w", err)
	}
	for i := range findings {
		findings[i].File = manifest
	}
	return findings, nil
}

// pipAuditVuln is one entry of a dependency's "vulns" array.
type pipAuditVuln struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// pipAuditDependency is one entry of pip-audit --format json's top-level
// "dependencies" array.
type pipAuditDependency struct {
	Name    string         `json:"name"`
	Version string         `json:"version"`
	Vulns   []pipAuditVuln `json:"vulns"`
}

type pipAuditOutput struct {
	Dependencies []pipAuditDependency `json:"dependencies"`
}

// pipAuditFindings parses `pip-audit --format json` output: one finding per
// vulns[] entry, one entry per matched CVE/GHSA advisory on a dependency.
func pipAuditFindings(out []byte) ([]finding.Finding, error) {
	var parsed pipAuditOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("parsing pip-audit output: %w", err)
	}
	var findings []finding.Finding
	for _, dep := range parsed.Dependencies {
		for _, v := range dep.Vulns {
			findings = append(findings, finding.Finding{
				Line:     0,
				Tool:     "pip-audit",
				RuleID:   v.ID,
				Category: finding.CategorySecurity,
				// ponytail: pip-audit's JSON carries no severity field at
				// all (unlike npm/cargo audit) - flat SeverityHigh for
				// every confirmed CVE match, the same simplification
				// cargo-audit already makes for its own
				// vulnerabilities.list entries. Upgrade path: score via an
				// external feed (e.g. OSV.dev) if graded severity is
				// needed - explicitly out of scope for v1 (no extra
				// network calls beyond what pip-audit itself makes).
				Severity: finding.SeverityHigh,
				Message:  fmt.Sprintf("%s (%s %s)", v.Description, dep.Name, dep.Version),
			})
		}
	}
	return findings, nil
}
