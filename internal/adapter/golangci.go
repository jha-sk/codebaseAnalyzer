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

func (GolangciLint) Install() error {
	return exec.Command("go", "install", "github.com/golangci/golangci-lint/cmd/golangci-lint@latest").Run()
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

func (GolangciLint) Run(path string) ([]finding.Finding, error) {
	out, err := runCommand(path, "golangci-lint", "run", "--out-format", "json",
		"--enable", "govet,errcheck,staticcheck,contextcheck,bodyclose,noctx")
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
