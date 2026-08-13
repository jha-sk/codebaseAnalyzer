package adapter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"codebase-analyser/internal/finding"
)

// JSAudit scans a JS/TS project's dependency tree for known CVEs by
// dispatching to whichever package manager the project's lockfile names:
// npm, yarn or pnpm. It is one adapter rather than three: the package
// manager is a runtime detail of the same job (scan the lockfile for CVEs),
// not a different job. Three adapter types would triple the orchestrator
// wiring and, since only one manager can ever apply to a given lockfile,
// would produce two bogus "skipped tool" notes on every single repo.
//
// JSAudit does not implement adapter.Targeted: a dependency audit has no
// sub-unit to restrict to (one lockfile covers the whole project, not one
// per package or file). cargoaudit.go makes the same choice for the same
// reason.
type JSAudit struct{}

func (JSAudit) Name() string { return "js-audit" }

// CheckInstalled probes for npm specifically. Run dispatches to whichever
// manager the project's lockfile names, but npm is the one Node.js always
// ships, so its presence is what gates whether the orchestrator attempts
// this adapter at all. A yarn-or-pnpm repo on a machine with only npm still
// fails honestly inside Run (exec: "yarn": executable file not found in
// $PATH) rather than silently reporting zero CVEs.
func (JSAudit) CheckInstalled() bool { return commandExists("npm") }

// Install always fails: a package manager cannot bootstrap itself. Auditing
// uses the repo's own package manager, not the analyser's pinned tool cache
// (installJSTools in js.go installs ESLint/TypeScript for linting, which is
// a separate concern), so there is nothing here to install.
func (JSAudit) Install() error {
	return fmt.Errorf("npm not found on PATH; install Node.js to enable JS/TS dependency auditing")
}

// detectLockfile reports which lockfile (if any) is present directly in dir
// and the package manager it implies. Checked in npm/yarn/pnpm order, so
// npm wins if a repo somehow carries both a package-lock.json and a
// yarn.lock. Deliberately not findUp: in a workspace the lockfile lives
// only at the repo root, and Run relies on that to run the audit exactly
// once (see Run's comment).
func detectLockfile(dir string) (lockfile, manager string) {
	candidates := []struct{ file, manager string }{
		{"package-lock.json", "npm"},
		{"npm-shrinkwrap.json", "npm"},
		{"yarn.lock", "yarn"},
		{"pnpm-lock.yaml", "pnpm"},
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(dir, c.file)); err == nil {
			return c.file, c.manager
		}
	}
	return "", ""
}

// jsAuditSeverityTable maps the severity vocabulary shared by npm, yarn and
// pnpm audit output to the analyser's normalized severity.
var jsAuditSeverityTable = map[string]finding.Severity{
	"critical": finding.SeverityCritical,
	"high":     finding.SeverityHigh,
	"moderate": finding.SeverityMedium,
	"low":      finding.SeverityLow,
	"info":     finding.SeverityLow,
}

func jsSeverity(s string) finding.Severity {
	if sev, ok := jsAuditSeverityTable[s]; ok {
		return sev
	}
	return finding.SeverityMedium
}

func (JSAudit) Run(path string) ([]finding.Finding, error) {
	lockfile, manager := detectLockfile(path)
	if manager == "" {
		// No lockfile in this project dir is not an error: in a workspace
		// the lockfile lives only at the repo root, so every member package
		// would otherwise report a bogus "skipped tool" and drag the run's
		// exit code to 2. The root package is detected as its own project
		// and audits the shared lockfile there exactly once.
		return nil, nil
	}

	var findings []finding.Finding
	var err error
	switch manager {
	case "npm":
		var out []byte
		out, err = runCommand(path, "npm", "audit", "--json")
		if err != nil {
			return nil, fmt.Errorf("js-audit: %w", err)
		}
		findings, err = npmAuditFindings(out)
	case "yarn":
		var out []byte
		out, err = runCommand(path, "yarn", "audit", "--json")
		if err != nil {
			return nil, fmt.Errorf("js-audit: %w", err)
		}
		findings, err = yarnAuditFindings(bytes.NewReader(out))
	case "pnpm":
		var out []byte
		out, err = runCommand(path, "pnpm", "audit", "--json")
		if err != nil {
			return nil, fmt.Errorf("js-audit: %w", err)
		}
		findings, err = pnpmAuditFindings(out)
	}
	if err != nil {
		return nil, fmt.Errorf("js-audit: %w", err)
	}
	for i := range findings {
		findings[i].File = lockfile
	}
	return findings, nil
}

// npmVulnerability is the shape of one entry in npm audit --json's top-level
// "vulnerabilities" map, keyed by package name.
type npmVulnerability struct {
	Name     string            `json:"name"`
	Severity string            `json:"severity"`
	Via      []json.RawMessage `json:"via"`
}

type npmAuditOutput struct {
	Vulnerabilities map[string]npmVulnerability `json:"vulnerabilities"`
}

// npmAdvisory is one OBJECT entry of a npmVulnerability's "via" array: a
// real advisory. A bare-string entry in the same array is instead a
// transitive "vulnerable because a dependency is" link with no advisory id
// of its own, and is handled separately in npmAuditFindings.
type npmAdvisory struct {
	Source   int    `json:"source"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Severity string `json:"severity"`
}

// npmAdvisoryRuleID prefers the GHSA id embedded in the advisory URL (e.g.
// "https://github.com/advisories/GHSA-xvch-5gv4-984h") since that's the id
// consumers recognize; falls back to the numeric internal source id.
func npmAdvisoryRuleID(a npmAdvisory) string {
	if idx := bytes.Index([]byte(a.URL), []byte("GHSA-")); idx != -1 {
		return a.URL[idx:]
	}
	return strconv.Itoa(a.Source)
}

// npmAuditFindings parses `npm audit --json` output. via is a heterogeneous
// array: object entries are real advisories, bare-string entries are
// transitive "vulnerable because a dependency is" links. Only object
// entries turn into per-advisory findings — string entries would produce
// duplicate findings with no advisory id. A package whose via is entirely
// strings still gets exactly ONE finding, using the package-level severity
// and a message naming the transitive path, so it isn't silently dropped.
func npmAuditFindings(data []byte) ([]finding.Finding, error) {
	var parsed npmAuditOutput
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parsing npm audit output: %w", err)
	}
	return npmVulnerabilitiesToFindings(parsed.Vulnerabilities), nil
}

// npmVulnerabilitiesToFindings turns a decoded npm-v7+-shape "vulnerabilities"
// map into findings. Shared by npmAuditFindings and pnpmAuditFindings: pnpm
// versions that report through the npm-v7-style backend emit this identical
// map shape under the same "vulnerabilities" key, so there is no separate
// pnpm parser to write for that case.
func npmVulnerabilitiesToFindings(vulnerabilities map[string]npmVulnerability) []finding.Finding {
	// Sort package names for deterministic output; map iteration order is
	// random.
	names := make([]string, 0, len(vulnerabilities))
	for name := range vulnerabilities {
		names = append(names, name)
	}
	sort.Strings(names)

	var findings []finding.Finding
	for _, name := range names {
		vuln := vulnerabilities[name]
		var transitiveVia []string
		sawAdvisory := false
		for _, raw := range vuln.Via {
			var viaName string
			if err := json.Unmarshal(raw, &viaName); err == nil {
				transitiveVia = append(transitiveVia, viaName)
				continue
			}
			var adv npmAdvisory
			if err := json.Unmarshal(raw, &adv); err == nil {
				sawAdvisory = true
				findings = append(findings, finding.Finding{
					Line:     0,
					Tool:     "js-audit",
					RuleID:   npmAdvisoryRuleID(adv),
					Category: finding.CategorySecurity,
					Severity: jsSeverity(adv.Severity),
					Message:  fmt.Sprintf("%s (%s)", adv.Title, vuln.Name),
				})
			}
		}
		if !sawAdvisory && len(transitiveVia) > 0 {
			via := transitiveVia[0]
			for _, v := range transitiveVia[1:] {
				via += ", " + v
			}
			findings = append(findings, finding.Finding{
				Line:     0,
				Tool:     "js-audit",
				RuleID:   "",
				Category: finding.CategorySecurity,
				Severity: jsSeverity(vuln.Severity),
				Message:  fmt.Sprintf("transitively vulnerable via %s (%s)", via, vuln.Name),
			})
		}
	}
	return findings
}

// yarnAuditLine is the shape of one NDJSON line from `yarn audit --json`.
// Only lines with type "auditAdvisory" carry a finding; every other type
// ("auditSummary", "info", ...) is ignored.
type yarnAuditLine struct {
	Type string `json:"type"`
	Data struct {
		Resolution struct {
			ID int `json:"id"`
		} `json:"resolution"`
		Advisory struct {
			ModuleName       string `json:"module_name"`
			Severity         string `json:"severity"`
			Title            string `json:"title"`
			GithubAdvisoryID string `json:"github_advisory_id"`
		} `json:"advisory"`
	} `json:"data"`
}

// yarnAuditFindings parses `yarn audit --json` NDJSON output, one JSON
// object per line (the same streaming shape clippy.go already decodes for
// cargo's NDJSON, but line-delimited here rather than a concatenated
// stream, so it's read with bufio.Scanner rather than json.Decoder). A line
// that isn't valid JSON, or is JSON of an unrecognized shape, is skipped
// rather than failing the whole parse — a trailing blank line or a stray
// log line from yarn must not drop every real advisory around it.
func yarnAuditFindings(r io.Reader) ([]finding.Finding, error) {
	var findings []finding.Finding
	scanner := bufio.NewScanner(r)
	// yarn audit lines carry full advisory text; grow past bufio.Scanner's
	// 64KiB default token limit.
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var l yarnAuditLine
		if err := json.Unmarshal(line, &l); err != nil {
			continue
		}
		if l.Type != "auditAdvisory" {
			continue
		}
		ruleID := l.Data.Advisory.GithubAdvisoryID
		if ruleID == "" {
			ruleID = strconv.Itoa(l.Data.Resolution.ID)
		}
		findings = append(findings, finding.Finding{
			Line:     0,
			Tool:     "js-audit",
			RuleID:   ruleID,
			Category: finding.CategorySecurity,
			Severity: jsSeverity(l.Data.Advisory.Severity),
			Message:  fmt.Sprintf("%s (%s)", l.Data.Advisory.Title, l.Data.Advisory.ModuleName),
		})
	}
	if err := scanner.Err(); err != nil {
		// Return what was already parsed alongside the error rather than
		// discarding it: a bad line further down the stream (e.g. one over
		// the 10MB buffer cap) must not drop every real advisory that scanned
		// cleanly before it, which is exactly the tolerance the per-line
		// unmarshal skip above already provides for a single malformed line.
		return findings, fmt.Errorf("parsing yarn audit output: %w", err)
	}
	return findings, nil
}

// pnpmAdvisory is one entry of pnpm audit --json's "advisories" map (legacy
// npm-v6 shape), keyed by numeric-string advisory id.
type pnpmAdvisory struct {
	ModuleName       string `json:"module_name"`
	Severity         string `json:"severity"`
	Title            string `json:"title"`
	GithubAdvisoryID string `json:"github_advisory_id"`
}

// pnpmAuditOutput covers both shapes pnpm audit --json is known to emit:
// the legacy npm-v6 "advisories" map (older pnpm / registries) and the
// npm-v7+ "vulnerabilities" map (newer pnpm, mirroring whatever shape its
// registry backend returns). Both fields decode from the same payload; only
// one is ever actually populated by a real tool run.
type pnpmAuditOutput struct {
	Advisories      map[string]pnpmAdvisory     `json:"advisories"`
	Vulnerabilities map[string]npmVulnerability `json:"vulnerabilities"`
}

// pnpmAuditFindings parses `pnpm audit --json` output, handling both shapes
// pnpmAuditOutput declares. The legacy "advisories" map (keyed by
// numeric-string id) wins when it has entries; a non-empty "vulnerabilities"
// map is the fallback for pnpm versions that emit the modern npm-v7-style
// shape instead. Getting this wrong is not cosmetic: json.Unmarshal against
// an unrecognised shape doesn't error — it silently leaves both maps at
// their zero value — so a naive "just read advisories" parser would report
// a CVE-riddled pnpm project as clean. To keep that failure loud instead of
// silent, an output that has NEITHER key at all (both maps nil, i.e. absent
// from the JSON, not merely empty) is treated as an unrecognised shape and
// returns an error rather than a quiet empty result. Advisories present but
// empty (a genuinely clean legacy report) still returns zero findings with
// no error.
func pnpmAuditFindings(data []byte) ([]finding.Finding, error) {
	var parsed pnpmAuditOutput
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parsing pnpm audit output: %w", err)
	}

	if len(parsed.Advisories) > 0 {
		return pnpmLegacyAdvisoriesToFindings(parsed.Advisories), nil
	}
	if parsed.Vulnerabilities != nil {
		return npmVulnerabilitiesToFindings(parsed.Vulnerabilities), nil
	}
	if parsed.Advisories != nil {
		// "advisories" key was present but empty: a genuinely clean legacy
		// report, not an unrecognised shape.
		return nil, nil
	}
	return nil, fmt.Errorf("pnpm audit: unrecognised output shape (no %q or %q key)", "advisories", "vulnerabilities")
}

// pnpmLegacyAdvisoriesToFindings turns the legacy npm-v6-shape "advisories"
// map into findings, iterated in SORTED key order so output is deterministic
// (map iteration order is random). cargoAuditFindings in cargoaudit.go
// solves the identical problem the identical way for cargo-audit's warnings
// map.
func pnpmLegacyAdvisoriesToFindings(advisories map[string]pnpmAdvisory) []finding.Finding {
	ids := make([]string, 0, len(advisories))
	for id := range advisories {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	findings := make([]finding.Finding, 0, len(ids))
	for _, id := range ids {
		adv := advisories[id]
		ruleID := adv.GithubAdvisoryID
		if ruleID == "" {
			ruleID = id
		}
		findings = append(findings, finding.Finding{
			Line:     0,
			Tool:     "js-audit",
			RuleID:   ruleID,
			Category: finding.CategorySecurity,
			Severity: jsSeverity(adv.Severity),
			Message:  fmt.Sprintf("%s (%s)", adv.Title, adv.ModuleName),
		})
	}
	return findings
}
