package adapter

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"codebase-analyser/internal/finding"
)

type GolangciLint struct{}

func (GolangciLint) Name() string         { return "golangci-lint" }
func (GolangciLint) CheckInstalled() bool { return commandExists("golangci-lint") }

// Install installs golangci-lint v2 specifically. `go install
// .../golangci-lint/cmd/golangci-lint@latest` (no /v2 in the module path)
// resolves to the latest v1.x tag, not v2 - Go modules requires a /v2
// segment to reach a v2+ module. Run below uses v2-only flags
// (--output.json.path, --default), so installing plain @latest here would
// "succeed" and then fail every real run with "unknown flag" - the same
// silent-success-then-broken-exec shape as the PATH/GOBIN bug this adapter
// package works around elsewhere (see resolveCommand in adapter.go).
func (GolangciLint) Install() error {
	return exec.Command("go", "install", "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest").Run()
}

type golangciOutput struct {
	Issues []golangciIssue `json:"Issues"`
}

type golangciIssue struct {
	FromLinter string `json:"FromLinter"`
	Text       string `json:"Text"`
	Pos        struct {
		Filename string `json:"Filename"`
		Line     int    `json:"Line"`
	} `json:"Pos"`
}

// linterCategory maps each explicitly-enabled linter to its default Finding category.
// ponytail: heuristic first pass, not a rule-by-rule table; revisit if a linter's
// findings consistently land in the wrong category.
var linterCategory = map[string]finding.Category{
	"govet":        finding.CategoryCorrectness,
	"errcheck":     finding.CategoryCorrectness,
	"staticcheck":  finding.CategoryCorrectness,
	"contextcheck": finding.CategoryOperational,
	"bodyclose":    finding.CategoryOperational,
	"noctx":        finding.CategoryOperational,
}

var linterSeverity = map[string]finding.Severity{
	"govet":        finding.SeverityHigh,
	"errcheck":     finding.SeverityHigh,
	"staticcheck":  finding.SeverityMedium,
	"contextcheck": finding.SeverityMedium,
	"bodyclose":    finding.SeverityMedium,
	"noctx":        finding.SeverityMedium,
}

// concurrencyRules lists staticcheck SA codes that specifically catch concurrency
// misuse (per the spec's concurrency scope note). Extend as more are identified.
var concurrencyRules = map[string]bool{
	"SA2001": true, // empty critical section
	"SA2002": true, // called testing.T.FailNow from a goroutine
}

// Targets golangci-lint v2 only (--out-format was a v1 flag, removed in v2;
// v2 replaces it with per-format --output.*.path flags). v2 also always
// writes a human-readable summary to stdout unless told otherwise
// (--show-stats defaults to true and a text formatter also defaults to
// stdout), which would otherwise land after the JSON object on the same
// stream and break json.Unmarshal ("invalid character ... after top-level
// value") - so both are redirected away from stdout, leaving stdout as pure
// JSON. Confirmed against a real v2.12.2 run that the Issues/FromLinter/
// Text/Pos.Filename/Pos.Line shape below still matches; no v1 support is
// attempted.
//
// --default none turns off v2's "standard" default linter set so only the
// six linters named in --enable run. Without it, golangci-lint v2 runs its
// standard set (errcheck, govet, staticcheck, unused, ...) alongside
// --enable rather than instead of it; confirmed against a real run that
// this leaks an "unused" finding through, which isn't in linterCategory/
// linterSeverity and so silently falls back to correctness/medium. The
// spec names exactly these six linters and explains why (three of them are
// the entire operational-category story), so restricting to that set is the
// intended scope rather than an accident of golangci-lint's defaults.
// (v1's equivalent flag was --disable-all, removed in v2.)
func (g GolangciLint) Run(path string) ([]finding.Finding, error) {
	return g.RunTargets(path, nil)
}

// RunTargets restricts the run to the given package patterns; empty targets
// means the whole module, which is what Run has always done. Verified
// against a real golangci-lint v2.12.2 run that a bare package directory
// (e.g. "./internal/detect") both restricts findings to that package and
// keeps the exact same Issues/FromLinter/Text/Pos.Filename/Pos.Line shape.
func (GolangciLint) RunTargets(path string, targets []string) ([]finding.Finding, error) {
	if len(targets) == 0 {
		targets = []string{"./..."}
	}
	args := append([]string{"run",
		"--output.json.path", "stdout",
		"--output.text.path", "stderr",
		"--show-stats=false",
		"--default", "none",
		"--enable", "govet,errcheck,staticcheck,contextcheck,bodyclose,noctx",
	}, targets...)
	out, err := runCommand(path, "golangci-lint", args...)
	if err != nil {
		return nil, fmt.Errorf("golangci-lint: %w", err)
	}
	var parsed golangciOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("golangci-lint: parsing output: %w", err)
	}
	return golangciFindings(parsed), nil
}

func golangciFindings(parsed golangciOutput) []finding.Finding {
	findings := make([]finding.Finding, 0, len(parsed.Issues))
	for _, issue := range parsed.Issues {
		ruleID := extractStaticcheckRule(issue.Text)
		category := linterCategory[issue.FromLinter]
		if category == "" {
			category = finding.CategoryCorrectness
		}
		if issue.FromLinter == "staticcheck" && concurrencyRules[ruleID] {
			category = finding.CategoryConcurrency
		}
		if issue.FromLinter == "govet" && isCopylocksMessage(issue.Text) {
			category = finding.CategoryConcurrency
		}
		severity := linterSeverity[issue.FromLinter]
		if severity == "" {
			severity = finding.SeverityMedium
		}
		findings = append(findings, finding.Finding{
			File:     issue.Pos.Filename,
			Line:     issue.Pos.Line,
			Tool:     "golangci-lint",
			RuleID:   pickRuleID(issue.FromLinter, ruleID),
			Category: category,
			Severity: severity,
			Message:  issue.Text,
		})
	}
	return findings
}

// extractStaticcheckRule pulls the leading "SAxxxx" code off a staticcheck message.
func extractStaticcheckRule(text string) string {
	if strings.HasPrefix(text, "SA") {
		if idx := strings.Index(text, ":"); idx > 0 {
			return text[:idx]
		}
	}
	return ""
}

// isCopylocksMessage reports whether text looks like one of govet's copylocks
// messages ("call of ... copies lock value", "literal copies lock value",
// "passes lock by value").
// ponytail: message-text heuristic, because golangci-lint's JSON output
// doesn't expose which vet analyzer produced a govet finding. Upgrade path:
// switch to a proper analyzer-name field if/when golangci-lint adds one.
func isCopylocksMessage(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "copies lock value") || strings.Contains(lower, "passes lock by value")
}

func pickRuleID(linter, ruleID string) string {
	if ruleID != "" {
		return ruleID
	}
	return linter
}
