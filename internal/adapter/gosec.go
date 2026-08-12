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
	Severity string `json:"severity"`
	RuleID   string `json:"rule_id"`
	Details  string `json:"details"`
	File     string `json:"file"`
	Line     string `json:"line"`
}

var gosecSeverity = map[string]finding.Severity{
	"HIGH":   finding.SeverityCritical,
	"MEDIUM": finding.SeverityHigh,
	"LOW":    finding.SeverityMedium,
}

func (Gosec) Run(path string) ([]finding.Finding, error) {
	out, err := runCommand(path, "gosec", "-fmt=json", "./...")
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
