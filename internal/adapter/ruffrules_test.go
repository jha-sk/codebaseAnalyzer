package adapter

import (
	"testing"

	"codebase-analyser/internal/finding"
)

func TestClassifyRuffCode(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		category finding.Category
		severity finding.Severity
	}{
		{"shell=True subprocess call is high security", "S602", finding.CategorySecurity, finding.SeverityHigh},
		{"exec-adjacent subprocess call is high security", "S603", finding.CategorySecurity, finding.SeverityHigh},
		{"shell-injection start_process is high security", "S605", finding.CategorySecurity, finding.SeverityHigh},
		{"partial-path subprocess call is medium security", "S607", finding.CategorySecurity, finding.SeverityMedium},
		{"undefined name is high correctness", "F821", finding.CategoryCorrectness, finding.SeverityHigh},
		{"unused import is low correctness", "F401", finding.CategoryCorrectness, finding.SeverityLow},
		{"unknown security-family code keeps the category", "S999", finding.CategorySecurity, finding.SeverityMedium},
		{"unknown pyflakes-family code keeps the category", "F999", finding.CategoryCorrectness, finding.SeverityMedium},
		{"totally unknown prefix defaults to correctness/medium", "PLW1510", finding.CategoryCorrectness, finding.SeverityMedium},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			category, severity := classifyRuffCode(tc.code)
			if category != tc.category || severity != tc.severity {
				t.Errorf("classifyRuffCode(%q) = (%v, %v), want (%v, %v)", tc.code, category, severity, tc.category, tc.severity)
			}
		})
	}
}
