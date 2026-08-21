package adapter

import (
	"strings"

	"codebase-analyser/internal/finding"
)

// ruffClass mirrors eslintClass (eslintrules.go): ruff's own rule codes
// carry no severity signal of their own (confirmed live: ruff's JSON
// "severity" field is always the literal string "error" across every rule
// family), so category and severity both come entirely from this curated
// table, exact-match first then prefix-family fallback.
type ruffClass struct {
	Category finding.Category
	Severity finding.Severity
}

var ruffExactClasses = map[string]ruffClass{
	// Security - Bandit-derived "S" rules with genuinely different risk
	// profiles worth distinguishing individually.
	"S602": {finding.CategorySecurity, finding.SeverityHigh},   // subprocess with shell=True
	"S603": {finding.CategorySecurity, finding.SeverityHigh},   // subprocess without shell=True but untrusted input
	"S605": {finding.CategorySecurity, finding.SeverityHigh},   // os.system / os.popen style shell injection
	"S607": {finding.CategorySecurity, finding.SeverityMedium}, // partial executable path (PATH-dependent, lower blast radius)
	"S324": {finding.CategorySecurity, finding.SeverityMedium}, // insecure hash function (md5/sha1)
	"S105": {finding.CategorySecurity, finding.SeverityHigh},   // hardcoded password string

	// Correctness - pyflakes "F" rules that are genuinely bug-shaped.
	"F821": {finding.CategoryCorrectness, finding.SeverityHigh},   // undefined name
	"F811": {finding.CategoryCorrectness, finding.SeverityMedium}, // redefinition of unused name
	"F401": {finding.CategoryCorrectness, finding.SeverityLow},    // unused import
	"F841": {finding.CategoryCorrectness, finding.SeverityLow},    // unused local variable
}

// ruffPrefixClasses catches codes the exact table doesn't list: ruff groups
// rules into letter-prefixed families sharing one concern (S = security,
// F = pyflakes correctness), so an unrecognised member of a known family
// still lands in the right category rather than defaulting blind.
var ruffPrefixClasses = map[string]ruffClass{
	"S": {finding.CategorySecurity, finding.SeverityMedium},
	"F": {finding.CategoryCorrectness, finding.SeverityMedium},
}

// classifyRuffCode maps one ruff rule code to a category and severity:
// exact match, then prefix-family fallback, then an unclassified default.
// Mirrors classifyESLintRule's three-tier structure (eslintrules.go).
func classifyRuffCode(code string) (finding.Category, finding.Severity) {
	if c, ok := ruffExactClasses[code]; ok {
		return c.Category, c.Severity
	}
	for prefix, c := range ruffPrefixClasses {
		if strings.HasPrefix(code, prefix) {
			return c.Category, c.Severity
		}
	}
	return finding.CategoryCorrectness, finding.SeverityMedium
}
