package mcpserver

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase-analyser/internal/cache"
	"codebase-analyser/internal/detect"
	"codebase-analyser/internal/finding"
	"codebase-analyser/internal/orchestrator"
	"codebase-analyser/internal/report"
)

// AnalyzeInput is the tool's argument object. Every field is optional
// (`omitempty` keeps it out of the schema's required list), so an agent can
// call analyze_codebase with no arguments at all.
type AnalyzeInput struct {
	Path     string   `json:"path,omitempty" jsonschema:"path to the repository to analyse; defaults to the server's working directory"`
	Category []string `json:"category,omitempty" jsonschema:"restrict results to these categories: correctness, concurrency, security, operational"`
	Severity string   `json:"severity,omitempty" jsonschema:"only return findings at or above this severity: critical, high, medium, low"`
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
	Shown        int            `json:"shown"`
	Truncated    bool           `json:"truncated"`
	Note         string         `json:"note,omitempty"`
	Summary      map[string]int `json:"summary"`
	Categories   map[string]int `json:"categories"`
	Incomplete   bool           `json:"incomplete"`
	SkippedTools []SkippedTool  `json:"skippedTools"`
	Findings     []Finding      `json:"findings"`
}

// DefaultMaxFindings caps how many findings are returned in full detail.
// Counts in Summary/Categories/Total always cover everything.
const DefaultMaxFindings = 50

// filter applies the caller's category/severity narrowing. It runs before the
// cap so the cap always selects from what the caller actually asked for.
func filter(findings []finding.Finding, cats []finding.Category, min finding.Severity) []finding.Finding {
	allowed := map[finding.Category]bool{}
	for _, c := range cats {
		allowed[c] = true
	}
	var out []finding.Finding
	for _, f := range findings {
		if len(allowed) > 0 && !allowed[f.Category] {
			continue
		}
		if min != "" && !finding.MeetsThreshold(f.Severity, min) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// capFindings orders findings most-severe-first and returns at most max of
// them. The tiebreakers (file, line, ruleID) make the truncation
// deterministic: the same repo analysed twice returns the same 50.
func capFindings(fs []finding.Finding, max int) (shown []finding.Finding, truncated bool) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if ra, rb := finding.SeverityRank(a.Severity), finding.SeverityRank(b.Severity); ra != rb {
			return ra > rb
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.RuleID < b.RuleID
	})
	if len(fs) <= max {
		return fs, false
	}
	return fs[:max], true
}

// parseFilters validates the caller's narrowing arguments through the same
// parsers the CLI uses, so the two front doors accept exactly the same values.
func parseFilters(in AnalyzeInput) ([]finding.Category, finding.Severity, error) {
	var cats []finding.Category
	for _, c := range in.Category {
		cat, err := finding.ParseCategory(strings.TrimSpace(c))
		if err != nil {
			return nil, "", err
		}
		cats = append(cats, cat)
	}
	if in.Severity == "" {
		return cats, "", nil
	}
	sev, err := finding.ParseSeverity(strings.TrimSpace(in.Severity))
	if err != nil {
		return nil, "", err
	}
	return cats, sev, nil
}

func (s *Server) analyze(ctx context.Context, _ *mcp.CallToolRequest, in AnalyzeInput) (*mcp.CallToolResult, AnalyzeOutput, error) {
	path := in.Path
	if path == "" {
		path = "."
	}
	cats, minSev, err := parseFilters(in)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}

	projects, skippedPaths, err := detect.Detect(path)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	if len(projects) == 0 {
		return nil, AnalyzeOutput{}, fmt.Errorf("no analysable project found under %s (looked for go.mod, Cargo.toml, package.json)", path)
	}

	findings, skipped := s.runCached(path, projects)
	for _, p := range skippedPaths {
		skipped = append(skipped, SkippedTool{Reason: "unreadable path during detection: " + p})
	}
	findings = filter(findings, cats, minSev)

	// Remember the uncapped run for push_to_dashboard. It is
	// recorded after filtering deliberately: the agent asked for this view,
	// and pushing a different set than it just saw would be surprising.
	s.mu.Lock()
	s.last = &lastRun{path: path, findings: findings, skipped: toReportSkipped(skipped)}
	s.mu.Unlock()

	return nil, buildOutput(findings, skipped, s.maxFindings), nil
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

func buildOutput(findings []finding.Finding, skipped []SkippedTool, max int) AnalyzeOutput {
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
	// Count every finding before capping: the caller must be able to trust
	// the totals even when it only sees the top slice.
	for _, f := range findings {
		out.Summary[string(f.Severity)]++
		out.Categories[string(f.Category)]++
	}

	shown, truncated := capFindings(findings, max)
	out.Shown = len(shown)
	out.Truncated = truncated
	if truncated {
		out.Note = fmt.Sprintf(
			"showing the %d most severe of %d findings; %d not shown. Narrow with the category or severity arguments to see the rest.",
			len(shown), out.Total, out.Total-len(shown))
	}
	for _, f := range shown {
		out.Findings = append(out.Findings, Finding{
			File: f.File, Line: f.Line, Tool: f.Tool, RuleID: f.RuleID,
			Category: string(f.Category), Severity: string(f.Severity), Message: f.Message,
		})
	}
	return out
}

// toReportSkipped adapts the MCP wire type to the report package's own
// SkippedTool so RenderJSON can be reused verbatim.
func toReportSkipped(skipped []SkippedTool) []report.SkippedTool {
	out := make([]report.SkippedTool, 0, len(skipped))
	for _, s := range skipped {
		out = append(out, report.SkippedTool{Tool: s.Tool, Path: s.Path, Reason: s.Reason})
	}
	return out
}

// runCached runs every adapter for every project, serving unchanged
// package/crate results from disk. A cache that cannot be opened degrades to
// a full uncached run: a broken cache must never stop an analysis.
func (s *Server) runCached(rootPath string, projects []detect.Project) ([]finding.Finding, []SkippedTool) {
	store, err := cache.Open(rootPath)
	if err != nil {
		return collect(orchestrator.Run(projects, s.adapters))
	}
	defer store.Save()
	return collect(orchestrator.RunWithCache(projects, s.adapters, analysisCache{store: store}))
}

// analysisCache adapts the on-disk store to what the orchestrator needs.
type analysisCache struct{ store *cache.Store }

func (a analysisCache) Lookup(tool string, p detect.Project) (stale []string, cached []finding.Finding, ok bool) {
	units, err := cache.Units(p)
	if err != nil {
		return nil, nil, false
	}
	stamp := cache.ToolStamp(tool)
	for _, u := range units {
		fp, err := cache.Fingerprint(u.Dir, u.Exts)
		if err != nil {
			// One unreadable unit disables caching for this whole pair
			// rather than silently omitting it from the run.
			return nil, nil, false
		}
		if hit, found := a.store.Get(tool, stamp, u.Target, fp); found {
			cached = append(cached, hit...)
			continue
		}
		stale = append(stale, u.Target)
	}
	return stale, cached, true
}

func (a analysisCache) Store(tool string, p detect.Project, ran []string, produced []finding.Finding) {
	units, err := cache.Units(p)
	if err != nil {
		return
	}
	byTarget := map[string]cache.Unit{}
	for _, u := range units {
		byTarget[u.Target] = u
	}
	grouped := map[string][]finding.Finding{}
	for _, f := range produced {
		target := unitFor(f, units, p.Path)
		grouped[target] = append(grouped[target], f)
	}
	stamp := cache.ToolStamp(tool)
	for _, target := range ran {
		u, ok := byTarget[target]
		if !ok {
			continue
		}
		fp, err := cache.Fingerprint(u.Dir, u.Exts)
		if err != nil {
			continue
		}
		// Recorded even when grouped[target] is empty: "this package is
		// clean" is the common result and the one most worth caching.
		_ = a.store.Put(tool, stamp, target, fp, grouped[target])
	}
}

// unitFor maps a finding back to the unit that owns it, by matching its
// file's directory against the unit directories. A finding that matches
// nothing (a tool-level diagnostic with no file) is attributed to the project
// root so it is not silently dropped from the cache.
func unitFor(f finding.Finding, units []cache.Unit, root string) string {
	dir := filepath.Dir(filepath.Join(root, f.File))
	for _, u := range units {
		if u.Dir == dir {
			return u.Target
		}
	}
	return "./"
}
