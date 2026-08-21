package adapter

import (
	"testing"

	"codebase-analyser/internal/finding"
)

func TestClassifyMypyCode(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		severity finding.Severity
	}{
		{"arg-type is high", "arg-type", finding.SeverityHigh},
		{"assignment is high", "assignment", finding.SeverityHigh},
		{"return-value is high", "return-value", finding.SeverityHigh},
		{"union-attr is medium", "union-attr", finding.SeverityMedium},
		{"no-any-return is low", "no-any-return", finding.SeverityLow},
		{"unused-ignore is low", "unused-ignore", finding.SeverityLow},
		{"unrecognized code defaults to medium", "some-future-code", finding.SeverityMedium},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyMypyCode(tc.code); got != tc.severity {
				t.Errorf("classifyMypyCode(%q) = %v, want %v", tc.code, got, tc.severity)
			}
		})
	}
}
