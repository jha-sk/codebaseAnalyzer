package adapter

import (
	"encoding/json"
	"fmt"

	"codebase-analyser/internal/finding"
)

// Ruff wraps `ruff check`. Not Targeted: single-package only in v1, no
// natural sub-unit to restrict a run to - same reasoning as ESLint.
type Ruff struct{}

func (Ruff) Name() string         { return "ruff" }
func (Ruff) CheckInstalled() bool { return isExecutable(pinnedPyBin("ruff")) }
func (Ruff) Install() error       { return installPyTools() }

func (Ruff) Run(path string) ([]finding.Finding, error) {
	bin := pinnedPyBin("ruff")
	// --extend-select S ADDS the Bandit-derived security ruleset on top of
	// whatever the repo's own [tool.ruff] config selects (or ruff's E4/E7/
	// E9/F default, if it has none) - confirmed live that ruff's bare
	// default selection does NOT include S, so a plain `ruff check` against
	// a repo with no config would silently produce zero security findings,
	// contradicting the "no separate Bandit invocation" design intent.
	// --extend-select (not --select) is what preserves the rest of a
	// repo's own selection instead of replacing it.
	out, err := runCommand(path, bin, "check", "--output-format", "json", "--extend-select", "S", ".")
	if err != nil {
		return nil, fmt.Errorf("ruff: %w", err)
	}
	return ruffFindings(out)
}

type ruffLocation struct {
	Row int `json:"row"`
}

type ruffViolation struct {
	Code     string       `json:"code"`
	Filename string       `json:"filename"`
	Message  string       `json:"message"`
	Location ruffLocation `json:"location"`
}

// ruffFindings parses `ruff check --output-format json`'s output: a flat
// JSON array of violations. Ruff's own "severity" field is always "error"
// (confirmed live across F/S rule families) and carries no signal, so it is
// deliberately not parsed - classifyRuffCode's curated table is the only
// source of category/severity.
func ruffFindings(out []byte) ([]finding.Finding, error) {
	var violations []ruffViolation
	if err := json.Unmarshal(out, &violations); err != nil {
		return nil, fmt.Errorf("parsing ruff output: %w", err)
	}
	findings := make([]finding.Finding, 0, len(violations))
	for _, v := range violations {
		category, severity := classifyRuffCode(v.Code)
		findings = append(findings, finding.Finding{
			File:     v.Filename,
			Line:     v.Location.Row,
			Tool:     "ruff",
			RuleID:   v.Code,
			Category: category,
			Severity: severity,
			Message:  v.Message,
		})
	}
	return findings, nil
}
