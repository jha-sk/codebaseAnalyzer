package report

import (
	"encoding/json"
	"io"

	"codebase-analyser/internal/finding"
)

type jsonFinding struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Tool        string `json:"tool"`
	RuleID      string `json:"ruleID"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
	Explanation string `json:"explanation"`
}

type jsonReport struct {
	Summary  map[string]int `json:"summary"`
	Findings []jsonFinding  `json:"findings"`
}

func RenderJSON(w io.Writer, findings []finding.ExplainedFinding) error {
	counts := Summary(findings)
	out := jsonReport{
		Summary: map[string]int{
			"critical": counts[finding.SeverityCritical],
			"high":     counts[finding.SeverityHigh],
			"medium":   counts[finding.SeverityMedium],
			"low":      counts[finding.SeverityLow],
		},
		Findings: []jsonFinding{},
	}
	for _, f := range findings {
		out.Findings = append(out.Findings, jsonFinding{
			File: f.File, Line: f.Line, Tool: f.Tool, RuleID: f.RuleID,
			Category: string(f.Category), Severity: string(f.Severity),
			Message: f.Message, Explanation: f.Explanation,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // findings routinely contain "<-chan"; escaping makes raw output unreadable
	return enc.Encode(out)
}
