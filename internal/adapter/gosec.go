package adapter

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"codebase-analyser/internal/finding"
)

type Gosec struct{}

func (Gosec) Name() string         { return "gosec" }
func (Gosec) CheckInstalled() bool { return commandExists("gosec") }

func (Gosec) Install() error {
	return exec.Command("go", "install", "github.com/securego/gosec/v2/cmd/gosec@latest").Run()
}

type gosecOutput struct {
	Issues []gosecIssue `json:"Issues"`
}

type gosecIssue struct {
	Severity   string `json:"severity"`
	Confidence string `json:"confidence"`
	RuleID     string `json:"rule_id"`
	Details    string `json:"details"`
	File       string `json:"file"`
	Line       string `json:"line"`
}

var gosecSeverity = map[string]finding.Severity{
	"HIGH":   finding.SeverityCritical,
	"MEDIUM": finding.SeverityHigh,
	"LOW":    finding.SeverityMedium,
}

// gosecLowConfidenceDamp drops a LOW-confidence finding's severity by one
// level from what gosecSeverity alone would assign. LOW confidence is
// gosec's classic false-positive shape (e.g. G101's entropy heuristic
// flagging any sufficiently random-looking string) - without this, a
// HIGH-severity/LOW-confidence finding reports identically to a genuine
// HIGH/HIGH finding, which is worse than not distinguishing them at all.
var gosecLowConfidenceDamp = map[finding.Severity]finding.Severity{
	finding.SeverityCritical: finding.SeverityHigh,
	finding.SeverityHigh:     finding.SeverityMedium,
	finding.SeverityMedium:   finding.SeverityLow,
	finding.SeverityLow:      finding.SeverityLow,
}

func (g Gosec) Run(path string) ([]finding.Finding, error) {
	return g.RunTargets(path, nil)
}

// RunTargets restricts the run to the given package patterns; empty targets
// means the whole module, which is what Run has always done. Verified
// against a real gosec run that a bare package directory (e.g.
// "./internal/adapter") both restricts findings to that package and keeps
// the exact same Issues/file/line/rule_id shape.
func (Gosec) RunTargets(path string, targets []string) ([]finding.Finding, error) {
	if len(targets) == 0 {
		targets = []string{"./..."}
	}
	args := append([]string{"-fmt=json"}, targets...)
	out, err := runCommand(path, "gosec", args...)
	if err != nil {
		return nil, fmt.Errorf("gosec: %w", err)
	}
	var parsed gosecOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("gosec: parsing output: %w", err)
	}
	return gosecFindings(parsed), nil
}

func gosecFindings(parsed gosecOutput) []finding.Finding {
	findings := make([]finding.Finding, 0, len(parsed.Issues))
	for _, issue := range parsed.Issues {
		line, _ := strconv.Atoi(strings.SplitN(issue.Line, "-", 2)[0])
		severity := gosecSeverity[issue.Severity]
		if severity == "" {
			severity = finding.SeverityMedium
		}
		if issue.Confidence == "LOW" {
			severity = gosecLowConfidenceDamp[severity]
		}
		findings = append(findings, finding.Finding{
			File:     issue.File,
			Line:     line,
			Tool:     "gosec",
			RuleID:   issue.RuleID,
			Category: finding.CategorySecurity,
			Severity: severity,
			Message:  issue.Details,
		})
	}
	return findings
}
