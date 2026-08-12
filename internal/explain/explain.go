package explain

import (
	"context"

	"codebase-analyser/internal/finding"
)

type Explanation struct {
	Text       string
	FixPattern string
}

type Explainer interface {
	Explain(ctx context.Context, tool, ruleID, sampleMessage string, count int) (Explanation, error)
}

// Group groups findings by (tool, ruleID) and calls e.Explain once per group,
// attaching the shared explanation to every finding in it. A group whose call
// fails falls back to unexplained findings rather than blocking the rest.
func Group(ctx context.Context, e Explainer, findings []finding.Finding) []finding.ExplainedFinding {
	type key struct{ tool, rule string }
	groups := map[key][]int{}
	for i, f := range findings {
		k := key{f.Tool, f.RuleID}
		groups[k] = append(groups[k], i)
	}

	explained := finding.WithoutExplanation(findings)

	for k, idxs := range groups {
		exp, err := e.Explain(ctx, k.tool, k.rule, findings[idxs[0]].Message, len(idxs))
		if err != nil {
			continue
		}
		for _, i := range idxs {
			explained[i].Explanation = exp.Text
			explained[i].FixPattern = exp.FixPattern
		}
	}
	return explained
}
