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
	FixPattern  string `json:"fixPattern"`
}

type jsonSkippedTool struct {
	Tool   string `json:"tool"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type jsonReport struct {
	Summary map[string]int `json:"summary"`
	// Incomplete/SkippedTools live in the document itself, not just as a
	// stderr note or exit code, so a consumer that only reads stdout JSON
	// (the spec's explicit CI-tooling use case) can't mistake partial
	// coverage for a clean pass.
	Incomplete   bool              `json:"incomplete"`
	SkippedTools []jsonSkippedTool `json:"skippedTools"`
	Findings     []jsonFinding     `json:"findings"`
}

func RenderJSON(w io.Writer, findings []finding.ExplainedFinding, skipped []SkippedTool) error {
	counts := Summary(findings)
	out := jsonReport{
		Summary: map[string]int{
			"critical": counts[finding.SeverityCritical],
			"high":     counts[finding.SeverityHigh],
			"medium":   counts[finding.SeverityMedium],
			"low":      counts[finding.SeverityLow],
		},
		Incomplete:   len(skipped) > 0,
		SkippedTools: []jsonSkippedTool{},
		Findings:     []jsonFinding{},
	}
	for _, s := range skipped {
		out.SkippedTools = append(out.SkippedTools, jsonSkippedTool{Tool: s.Tool, Path: s.Path, Reason: s.Reason})
	}
	for _, f := range findings {
		out.Findings = append(out.Findings, jsonFinding{
			File: f.File, Line: f.Line, Tool: f.Tool, RuleID: f.RuleID,
			Category: string(f.Category), Severity: string(f.Severity),
			Message: f.Message, Explanation: f.Explanation, FixPattern: f.FixPattern,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // findings routinely contain "<-chan"; escaping makes raw output unreadable
	return enc.Encode(out)
}
