package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codebase-analyser/internal/finding"
)

// PipAudit scans a Python project's dependency manifest for known CVEs.
// Not Targeted: a dependency audit has no sub-unit to restrict to (one
// manifest covers the whole project) - same reasoning as CargoAudit/JSAudit.
type PipAudit struct{}

func (PipAudit) Name() string         { return "pip-audit" }
func (PipAudit) CheckInstalled() bool { return isExecutable(pinnedPyBin("pip-audit")) }
func (PipAudit) Install() error       { return installPyTools() }

// pipUnsupportedLocks are dependency-manifest formats a Python project may
// carry that pip-audit cannot read in v1, each with the reason surfaced as a
// named skip. Checked in this order, after requirements.txt.
//
// Running pip-audit anyway (no -r flag) is not an option: it then audits the
// interpreter running it - the analyser's own tool venv - not the target
// repo. That is simultaneously a false negative (the repo's real
// dependencies are never scanned) and a false positive (ruff/mypy/pip-audit's
// own transitive deps get attributed to the user's lockfile).
var pipUnsupportedLocks = []struct{ file, reason string }{
	{"poetry.lock", "poetry.lock-only project; pip-audit does not support Poetry's lockfile format natively in v1 - export a requirements.txt (e.g. `poetry export -f requirements.txt`) to enable dependency scanning"},
	{"uv.lock", "uv.lock-only project; pip-audit does not support uv's lockfile format natively in v1 - export a requirements.txt (e.g. `uv export --format requirements-txt`) to enable dependency scanning"},
	{"Pipfile.lock", "Pipenv-only project (Pipfile.lock present, no requirements.txt/poetry.lock/uv.lock); dependency scanning is not supported for Pipenv projects in v1"},
}

func pipFileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// pipAuditArgs is the one invocation pip-audit ever gets, kept separate from
// the run so its exact shape is assertable in a unit test.
//
// --no-deps audits exactly the lines requirements.txt pins instead of
// resolving the transitive tree, and --disable-pip stops pip-audit from
// creating a temp venv and running a real `pip install -r` to do that
// resolution. Both are needed: verified live on pip-audit 2.9.0 that
// --no-deps ALONE still installs transitive deps and still executes a local
// package's setup.py. Together they are the spec's "no dependency
// installation" containment rule - a Python package can run arbitrary code at
// install time via setup.py, and an analysed repo is untrusted input.
//
// The cost is that --disable-pip requires every requirement pinned with ==;
// Run turns pip-audit's error for an unpinned line into a named skip.
func pipAuditArgs(manifest string) []string {
	return []string{"-r", manifest, "--no-deps", "--disable-pip", "--format", "json"}
}

func (PipAudit) Run(path string) ([]finding.Finding, error) {
	// requirements.txt is the only format pip-audit reads directly (-r).
	const manifest = "requirements.txt"
	if !pipFileExists(path, manifest) {
		for _, l := range pipUnsupportedLocks {
			// There IS a dependency set here, just one pip-audit can't read -
			// report it as a named, actionable skip (spec: "reports
			// dependency-scanning as skipped, with a clear reason - not
			// silently absent"), mirroring jsaudit.go's error-vs-silence split.
			if pipFileExists(path, l.file) {
				return nil, fmt.Errorf("pip-audit: %s", l.reason)
			}
		}
		// No manifest of any kind: nothing to audit, healthy silence.
		return nil, nil
	}

	bin := pinnedPyBin("pip-audit")
	out, err := runCommand(path, bin, pipAuditArgs(manifest)...)
	if err != nil {
		// --disable-pip only works on a fully pinned requirements.txt; an
		// unpinned line makes pip-audit hard-fail with an empty stdout. Turn
		// that into the same named, actionable skip as the unsupported
		// lockfiles above rather than a generic "tool exited 1".
		//
		// Matched on the shorter "is not pinned": pip-audit 2.9.0 emits both
		// "requirement X is not pinned:" (no version at all) and "... is not
		// pinned to an exact version:" (a >= range), and both mean the same
		// thing here. runCommand carries pip-audit's stderr into err.
		if strings.Contains(err.Error(), "is not pinned") {
			return nil, fmt.Errorf("pip-audit: requirements.txt contains an unpinned requirement; --disable-pip (required for containment) needs every line pinned to an exact version (==) - pin all dependencies to enable dependency scanning")
		}
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
