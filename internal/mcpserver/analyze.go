package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase-analyser/internal/detect"
	"codebase-analyser/internal/finding"
	"codebase-analyser/internal/orchestrator"
)

// AnalyzeInput is the tool's argument object. Every field is optional
// (`omitempty` keeps it out of the schema's required list), so an agent can
// call analyze_codebase with no arguments at all.
type AnalyzeInput struct {
	Path string `json:"path,omitempty" jsonschema:"path to the repository to analyse; defaults to the server's working directory"`
}

// Finding is the wire shape of one finding. It deliberately omits the
// explanation/fixPattern fields the CLI's JSON report carries: the caller of
// this tool is itself an LLM and does its own explaining.
type Finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Tool     string `json:"tool"`
	RuleID   string `json:"ruleID"`
	Category string `json:"category"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// SkippedTool records a tool that did not run, so partial coverage can never
// be mistaken for a clean pass.
type SkippedTool struct {
	Tool   string `json:"tool"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type AnalyzeOutput struct {
	Total        int            `json:"total"`
	Summary      map[string]int `json:"summary"`
	Categories   map[string]int `json:"categories"`
	Incomplete   bool           `json:"incomplete"`
	SkippedTools []SkippedTool  `json:"skippedTools"`
	Findings     []Finding      `json:"findings"`
}

func (s *Server) analyze(ctx context.Context, _ *mcp.CallToolRequest, in AnalyzeInput) (*mcp.CallToolResult, AnalyzeOutput, error) {
	path := in.Path
	if path == "" {
		path = "."
	}

	projects, skippedPaths, err := detect.Detect(path)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	if len(projects) == 0 {
		return nil, AnalyzeOutput{}, fmt.Errorf("no Go or Rust project found under %s", path)
	}

	findings, skipped := collect(orchestrator.Run(projects, s.adapters))
	for _, p := range skippedPaths {
		skipped = append(skipped, SkippedTool{Reason: "unreadable path during detection: " + p})
	}

	return nil, buildOutput(findings, skipped), nil
}

// collect splits the orchestrator's per-tool results into findings and
// skip records, mirroring what cli.Execute does with the same slice.
func collect(results []orchestrator.ToolResult) ([]finding.Finding, []SkippedTool) {
	var findings []finding.Finding
	var skipped []SkippedTool
	for _, r := range results {
		if r.Skipped {
			skipped = append(skipped, SkippedTool{Tool: r.Tool, Path: r.Path, Reason: r.Error.Error()})
			continue
		}
		findings = append(findings, r.Findings...)
	}
	return findings, skipped
}

func buildOutput(findings []finding.Finding, skipped []SkippedTool) AnalyzeOutput {
	out := AnalyzeOutput{
		Total:        len(findings),
		Summary:      map[string]int{},
		Categories:   map[string]int{},
		Incomplete:   len(skipped) > 0,
		SkippedTools: skipped,
		Findings:     []Finding{},
	}
	if out.SkippedTools == nil {
		out.SkippedTools = []SkippedTool{}
	}
	for _, f := range findings {
		out.Summary[string(f.Severity)]++
		out.Categories[string(f.Category)]++
		out.Findings = append(out.Findings, Finding{
			File: f.File, Line: f.Line, Tool: f.Tool, RuleID: f.RuleID,
			Category: string(f.Category), Severity: string(f.Severity), Message: f.Message,
		})
	}
	return out
}
