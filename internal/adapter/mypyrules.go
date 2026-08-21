package adapter

import "codebase-analyser/internal/finding"

// mypySeverity maps a mypy error code to a severity. mypy's category is
// fixed at CategoryCorrectness per spec ("mypy | Correctness (type
// errors)") - only severity varies, by error code, curated the same way
// ruff's and ESLint's tables are.
var mypySeverity = map[string]finding.Severity{
	"arg-type":       finding.SeverityHigh,
	"assignment":     finding.SeverityHigh,
	"return-value":   finding.SeverityHigh,
	"call-arg":       finding.SeverityHigh,
	"union-attr":     finding.SeverityMedium,
	"attr-defined":   finding.SeverityMedium,
	"index":          finding.SeverityMedium,
	"no-any-return":  finding.SeverityLow,
	"unused-ignore":  finding.SeverityLow,
	"no-untyped-def": finding.SeverityLow,
}

// classifyMypyCode returns mypySeverity's entry for code, or SeverityMedium
// for anything not in the table - mypy adds error codes over time and an
// unrecognised one should land in the middle rather than being silently
// dropped or over/under-weighted.
func classifyMypyCode(code string) finding.Severity {
	if s, ok := mypySeverity[code]; ok {
		return s
	}
	return finding.SeverityMedium
}
