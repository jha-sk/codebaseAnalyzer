package adapter

import "codebase-analyser/internal/finding"

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

	// S101 (bare `assert`) sits in the Bandit-derived S family but is not a
	// vulnerability: assertions are stripped under `python -O`, so relying on
	// one for control flow is a robustness bug, not an attack surface. Low
	// because it fires on essentially every test file - reporting each one at
	// medium security would drown the genuine S findings above.
	"S101": {finding.CategoryCorrectness, finding.SeverityLow},

	// Correctness - pyflakes "F" rules that are genuinely bug-shaped.
	"F821": {finding.CategoryCorrectness, finding.SeverityHigh},   // undefined name
	"F811": {finding.CategoryCorrectness, finding.SeverityMedium}, // redefinition of unused name
	"F401": {finding.CategoryCorrectness, finding.SeverityLow},    // unused import
	"F841": {finding.CategoryCorrectness, finding.SeverityLow},    // unused local variable
}

// ruffFamilyClasses catches codes the exact table doesn't list: ruff groups
// rules into letter-prefixed families sharing one concern, so an
// unrecognised member of a known family still lands in the right category
// rather than defaulting blind.
//
// The style/modernization families are all (correctness, low): ruff's default
// selection is ~440 rules across dozens of families, and an import-sort or
// quote-style nit reported at the generic medium default is more severe than
// it is true. RUF is deliberately absent - some RUF rules are real bugs, so
// the family keeps the medium default until individual codes earn an exact
// entry.
var ruffFamilyClasses = map[string]ruffClass{
	"S": {finding.CategorySecurity, finding.SeverityMedium},
	"F": {finding.CategoryCorrectness, finding.SeverityMedium},

	"I":   {finding.CategoryCorrectness, finding.SeverityLow}, // isort
	"UP":  {finding.CategoryCorrectness, finding.SeverityLow}, // pyupgrade
	"D":   {finding.CategoryCorrectness, finding.SeverityLow}, // pydocstyle
	"N":   {finding.CategoryCorrectness, finding.SeverityLow}, // pep8-naming
	"E":   {finding.CategoryCorrectness, finding.SeverityLow}, // pycodestyle errors
	"W":   {finding.CategoryCorrectness, finding.SeverityLow}, // pycodestyle warnings
	"COM": {finding.CategoryCorrectness, finding.SeverityLow}, // flake8-commas
	"Q":   {finding.CategoryCorrectness, finding.SeverityLow}, // flake8-quotes
	"ISC": {finding.CategoryCorrectness, finding.SeverityLow}, // implicit str concat
}

// ruffCodeFamily splits a rule code into its leading letters: S602 -> "S",
// UP007 -> "UP", ISC001 -> "ISC". An exact family lookup beats a HasPrefix
// scan now that the table holds overlapping prefixes - HasPrefix would let
// ISC001 match "I", DTZ003 match "D" and SIM115 match "S", with map
// iteration order picking the winner.
func ruffCodeFamily(code string) string {
	for i, r := range code {
		if r < 'A' || r > 'Z' {
			return code[:i]
		}
	}
	return code
}

// classifyRuffCode maps one ruff rule code to a category and severity:
// exact match, then rule-family fallback, then an unclassified default.
// Mirrors classifyESLintRule's three-tier structure (eslintrules.go).
func classifyRuffCode(code string) (finding.Category, finding.Severity) {
	if c, ok := ruffExactClasses[code]; ok {
		return c.Category, c.Severity
	}
	if c, ok := ruffFamilyClasses[ruffCodeFamily(code)]; ok {
		return c.Category, c.Severity
	}
	return finding.CategoryCorrectness, finding.SeverityMedium
}
