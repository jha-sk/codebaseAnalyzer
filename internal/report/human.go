package report

import (
	"fmt"
	"io"

	"codebase-analyser/internal/finding"
)

// SkippedTool records one tool that didn't run for one project (install
// failure, crash, timeout, missing binary), so both renderers can make
// incomplete coverage visible in the report itself - not just via the exit
// code or a note that's easy to miss when only the report body is captured.
type SkippedTool struct {
	Tool   string
	Path   string
	Reason string
}

func Summary(findings []finding.ExplainedFinding) map[finding.Severity]int {
	counts := map[finding.Severity]int{}
	for _, f := range findings {
		counts[f.Severity]++
	}
	return counts
}

var categoryOrder = []finding.Category{
	finding.CategoryCorrectness, finding.CategoryConcurrency, finding.CategorySecurity, finding.CategoryOperational,
}
var severityOrder = []finding.Severity{
	finding.SeverityCritical, finding.SeverityHigh, finding.SeverityMedium, finding.SeverityLow,
}

var knownCategories = map[finding.Category]bool{
	finding.CategoryCorrectness: true, finding.CategoryConcurrency: true,
	finding.CategorySecurity: true, finding.CategoryOperational: true,
}
var knownSeverities = map[finding.Severity]bool{
	finding.SeverityCritical: true, finding.SeverityHigh: true,
	finding.SeverityMedium: true, finding.SeverityLow: true,
}

func RenderHuman(w io.Writer, findings []finding.ExplainedFinding, skipped []SkippedTool) {
	counts := Summary(findings)
	fmt.Fprintf(w, "Summary: critical=%d high=%d medium=%d low=%d\n",
		counts[finding.SeverityCritical], counts[finding.SeverityHigh],
		counts[finding.SeverityMedium], counts[finding.SeverityLow])
	if len(skipped) > 0 {
		fmt.Fprintf(w, "COVERAGE INCOMPLETE: %d tool run(s) skipped, findings below may be incomplete:\n", len(skipped))
		for _, s := range skipped {
			fmt.Fprintf(w, "  - %s (%s): %s\n", s.Tool, s.Path, s.Reason)
		}
	}
	fmt.Fprintln(w)

	byCategory := map[finding.Category][]finding.ExplainedFinding{}
	for _, f := range findings {
		byCategory[f.Category] = append(byCategory[f.Category], f)
	}

	for _, category := range categoryOrder {
		group := byCategory[category]
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(w, "== %s ==\n", category)
		renderCategory(w, group)
		fmt.Fprintln(w)
	}

	// Findings with a category outside the known enum would otherwise vanish
	// silently while still being counted in the summary line above. Surface
	// them under a trailing bucket instead of dropping them.
	var other []finding.ExplainedFinding
	for _, f := range findings {
		if !knownCategories[f.Category] {
			other = append(other, f)
		}
	}
	if len(other) > 0 {
		fmt.Fprintf(w, "== other ==\n")
		renderCategory(w, other)
		fmt.Fprintln(w)
	}
}

func renderCategory(w io.Writer, group []finding.ExplainedFinding) {
	bySeverity := map[finding.Severity][]finding.ExplainedFinding{}
	var extraSeverities []finding.Severity
	seenExtra := map[finding.Severity]bool{}
	for _, f := range group {
		bySeverity[f.Severity] = append(bySeverity[f.Severity], f)
		if !knownSeverities[f.Severity] && !seenExtra[f.Severity] {
			seenExtra[f.Severity] = true
			extraSeverities = append(extraSeverities, f.Severity)
		}
	}
	for _, sev := range severityOrder {
		sevGroup := bySeverity[sev]
		if len(sevGroup) == 0 {
			continue
		}
		renderRules(w, sev, sevGroup)
	}
	// Same rationale as the category fallback above: an unrecognized
	// severity must still show up rather than being silently dropped.
	for _, sev := range extraSeverities {
		renderRules(w, sev, bySeverity[sev])
	}
}

func renderRules(w io.Writer, sev finding.Severity, sevGroup []finding.ExplainedFinding) {
	byRule := map[string][]finding.ExplainedFinding{}
	var ruleOrder []string
	for _, f := range sevGroup {
		key := f.Tool + "/" + f.RuleID
		if _, seen := byRule[key]; !seen {
			ruleOrder = append(ruleOrder, key)
		}
		byRule[key] = append(byRule[key], f)
	}
	for _, rule := range ruleOrder {
		items := byRule[rule]
		fmt.Fprintf(w, "  [%s] %s (%d)\n", sev, rule, len(items))
		if items[0].Explanation != "" {
			fmt.Fprintf(w, "    %s\n", items[0].Explanation)
		}
		if items[0].FixPattern != "" {
			fmt.Fprintf(w, "    Fix: %s\n", items[0].FixPattern)
		}
		for _, f := range items {
			fmt.Fprintf(w, "    - %s:%d %s\n", f.File, f.Line, f.Message)
		}
	}
}
