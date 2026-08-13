package adapter

import (
	"testing"

	"codebase-analyser/internal/finding"
)

func TestClassifyESLintRule(t *testing.T) {
	tests := []struct {
		name     string
		ruleID   string
		level    int
		fatal    bool
		category finding.Category
		severity finding.Severity
	}{
		{"eval is critical security", "no-eval", 2, false, finding.CategorySecurity, finding.SeverityCritical},
		{"child_process is high security", "security/detect-child-process", 2, false, finding.CategorySecurity, finding.SeverityHigh},
		{"floating promise is concurrency", "@typescript-eslint/no-floating-promises", 2, false, finding.CategoryConcurrency, finding.SeverityHigh},
		{"catch-or-return is concurrency", "promise/catch-or-return", 2, false, finding.CategoryConcurrency, finding.SeverityHigh},
		{"async promise executor is concurrency", "no-async-promise-executor", 2, false, finding.CategoryConcurrency, finding.SeverityHigh},
		{"undefined variable is correctness", "no-undef", 2, false, finding.CategoryCorrectness, finding.SeverityHigh},
		{"debugger is operational", "no-debugger", 2, false, finding.CategoryOperational, finding.SeverityMedium},
		{"console is operational and low", "no-console", 2, false, finding.CategoryOperational, finding.SeverityLow},

		// Prefix fallback: a rule the table has never heard of still lands in
		// its plugin's category rather than defaulting to correctness.
		{"unknown security rule keeps the category", "security/detect-brand-new-thing", 2, false, finding.CategorySecurity, finding.SeverityMedium},
		{"unknown promise rule keeps the category", "promise/some-future-rule", 1, false, finding.CategoryConcurrency, finding.SeverityLow},

		// Level fallback, damped so an unmapped rule can't outrank a mapped bug.
		{"unmapped error", "unicorn/prefer-node-protocol", 2, false, finding.CategoryCorrectness, finding.SeverityMedium},
		{"unmapped warn", "unicorn/prefer-node-protocol", 1, false, finding.CategoryCorrectness, finding.SeverityLow},
		{"unknown level", "unicorn/prefer-node-protocol", 7, false, finding.CategoryCorrectness, finding.SeverityLow},

		// A parse error means nothing in the file was linted at all.
		{"fatal message", "", 2, true, finding.CategoryCorrectness, finding.SeverityHigh},
		{"null ruleId", "", 2, false, finding.CategoryCorrectness, finding.SeverityHigh},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat, sev := classifyESLintRule(tt.ruleID, tt.level, tt.fatal)
			if cat != tt.category || sev != tt.severity {
				t.Errorf("classifyESLintRule(%q, %d, %v) = (%s, %s), want (%s, %s)",
					tt.ruleID, tt.level, tt.fatal, cat, sev, tt.category, tt.severity)
			}
		})
	}
}

// TestESLintRuleTableIsValid catches typos in the table itself: a category
// or severity string that no longer parses would silently produce findings
// the report can't filter or rank.
func TestESLintRuleTableIsValid(t *testing.T) {
	for ruleID, class := range eslintRuleClasses {
		if ruleID == "" {
			t.Error("table has an empty rule id")
		}
		if _, err := finding.ParseCategory(string(class.Category)); err != nil {
			t.Errorf("rule %q: %v", ruleID, err)
		}
		if _, err := finding.ParseSeverity(string(class.Severity)); err != nil {
			t.Errorf("rule %q: %v", ruleID, err)
		}
	}
	for prefix, cat := range eslintPluginCategories {
		if _, err := finding.ParseCategory(string(cat)); err != nil {
			t.Errorf("prefix %q: %v", prefix, err)
		}
	}
}
