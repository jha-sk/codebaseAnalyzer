package adapter

import (
	"strings"

	"codebase-analyser/internal/finding"
)

// ESLint natively distinguishes only warn (1) and error (2), and — unlike
// gosec — has no inherent sense that a security rule matters more than a
// style nit. Category and severity are therefore both assigned here, by a
// curated per-rule lookup, the same way the other tools' severities are
// mapped. The table is exact-match first, then a plugin-prefix fallback so a
// newly added rule in a known plugin still lands in the right category
// rather than defaulting to correctness.
type eslintClass struct {
	Category finding.Category
	Severity finding.Severity
}

var eslintRuleClasses = map[string]eslintClass{
	// Concurrency — floating promises and async mistakes.
	"@typescript-eslint/no-floating-promises": {finding.CategoryConcurrency, finding.SeverityHigh},
	"@typescript-eslint/no-misused-promises":  {finding.CategoryConcurrency, finding.SeverityHigh},
	"@typescript-eslint/await-thenable":       {finding.CategoryConcurrency, finding.SeverityMedium},
	"@typescript-eslint/require-await":        {finding.CategoryConcurrency, finding.SeverityLow},
	"@typescript-eslint/no-await-in-loop":     {finding.CategoryConcurrency, finding.SeverityLow},
	"promise/catch-or-return":                 {finding.CategoryConcurrency, finding.SeverityHigh},
	"promise/always-return":                   {finding.CategoryConcurrency, finding.SeverityMedium},
	"promise/no-return-wrap":                  {finding.CategoryConcurrency, finding.SeverityMedium},
	"promise/param-names":                     {finding.CategoryConcurrency, finding.SeverityLow},
	"promise/valid-params":                    {finding.CategoryConcurrency, finding.SeverityMedium},
	"promise/no-nesting":                      {finding.CategoryConcurrency, finding.SeverityLow},
	"promise/no-promise-in-callback":          {finding.CategoryConcurrency, finding.SeverityLow},
	"promise/no-new-statics":                  {finding.CategoryConcurrency, finding.SeverityMedium},
	"require-atomic-updates":                  {finding.CategoryConcurrency, finding.SeverityHigh},
	"no-await-in-loop":                        {finding.CategoryConcurrency, finding.SeverityLow},
	"no-async-promise-executor":               {finding.CategoryConcurrency, finding.SeverityHigh},
	"no-promise-executor-return":              {finding.CategoryConcurrency, finding.SeverityMedium},

	// Security.
	"security/detect-child-process":           {finding.CategorySecurity, finding.SeverityHigh},
	"security/detect-eval-with-expression":    {finding.CategorySecurity, finding.SeverityCritical},
	"security/detect-non-literal-require":     {finding.CategorySecurity, finding.SeverityHigh},
	"security/detect-non-literal-fs-filename": {finding.CategorySecurity, finding.SeverityMedium},
	"security/detect-unsafe-regex":            {finding.CategorySecurity, finding.SeverityHigh},
	"security/detect-buffer-noassert":         {finding.CategorySecurity, finding.SeverityHigh},
	"security/detect-possible-timing-attacks": {finding.CategorySecurity, finding.SeverityMedium},
	"security/detect-pseudoRandomBytes":       {finding.CategorySecurity, finding.SeverityHigh},
	"security/detect-new-buffer":              {finding.CategorySecurity, finding.SeverityMedium},
	"security/detect-object-injection":        {finding.CategorySecurity, finding.SeverityLow},
	"no-eval":                                 {finding.CategorySecurity, finding.SeverityCritical},
	"no-implied-eval":                         {finding.CategorySecurity, finding.SeverityCritical},
	"no-new-func":                             {finding.CategorySecurity, finding.SeverityHigh},
	"no-script-url":                           {finding.CategorySecurity, finding.SeverityHigh},

	// Correctness — the genuinely bug-shaped core rules, promoted above the
	// medium default that unmapped rules get.
	"no-undef":                           {finding.CategoryCorrectness, finding.SeverityHigh},
	"no-unreachable":                     {finding.CategoryCorrectness, finding.SeverityHigh},
	"no-dupe-keys":                       {finding.CategoryCorrectness, finding.SeverityHigh},
	"no-dupe-args":                       {finding.CategoryCorrectness, finding.SeverityHigh},
	"no-dupe-class-members":              {finding.CategoryCorrectness, finding.SeverityHigh},
	"no-duplicate-case":                  {finding.CategoryCorrectness, finding.SeverityHigh},
	"no-cond-assign":                     {finding.CategoryCorrectness, finding.SeverityHigh},
	"no-self-assign":                     {finding.CategoryCorrectness, finding.SeverityMedium},
	"no-self-compare":                    {finding.CategoryCorrectness, finding.SeverityMedium},
	"use-isnan":                          {finding.CategoryCorrectness, finding.SeverityHigh},
	"valid-typeof":                       {finding.CategoryCorrectness, finding.SeverityHigh},
	"no-constant-condition":              {finding.CategoryCorrectness, finding.SeverityMedium},
	"no-fallthrough":                     {finding.CategoryCorrectness, finding.SeverityMedium},
	"eqeqeq":                             {finding.CategoryCorrectness, finding.SeverityLow},
	"@typescript-eslint/no-explicit-any": {finding.CategoryCorrectness, finding.SeverityLow},
	"@typescript-eslint/no-unused-vars":  {finding.CategoryCorrectness, finding.SeverityLow},
	"no-unused-vars":                     {finding.CategoryCorrectness, finding.SeverityLow},
	"@typescript-eslint/no-non-null-assertion": {finding.CategoryCorrectness, finding.SeverityMedium},
	"@typescript-eslint/no-unsafe-argument":    {finding.CategoryCorrectness, finding.SeverityMedium},

	// Operational — things that only bite in production.
	"no-console":            {finding.CategoryOperational, finding.SeverityLow},
	"no-debugger":           {finding.CategoryOperational, finding.SeverityMedium},
	"no-alert":              {finding.CategoryOperational, finding.SeverityLow},
	"no-process-exit":       {finding.CategoryOperational, finding.SeverityMedium},
	"n/no-process-exit":     {finding.CategoryOperational, finding.SeverityMedium},
	"no-empty":              {finding.CategoryOperational, finding.SeverityLow},
	"no-unsafe-finally":     {finding.CategoryOperational, finding.SeverityHigh},
	"no-ex-assign":          {finding.CategoryOperational, finding.SeverityMedium},
	"handle-callback-err":   {finding.CategoryOperational, finding.SeverityMedium},
	"n/handle-callback-err": {finding.CategoryOperational, finding.SeverityMedium},
}

// eslintPluginCategories catches rules the exact table doesn't list: a
// plugin's whole rule set shares a concern, so an unrecognised
// `security/...` rule is still a security finding rather than a correctness
// one. Prefixes are checked longest-first only in the sense that the map is
// disjoint — no prefix here is a prefix of another.
var eslintPluginCategories = map[string]finding.Category{
	"security/":   finding.CategorySecurity,
	"promise/":    finding.CategoryConcurrency,
	"no-secrets/": finding.CategorySecurity,
}

// eslintLevelSeverity is the fallback when a rule isn't classified: ESLint's
// own error/warn split, damped to medium/low so an unmapped style rule
// configured as "error" by a repo can never outrank a mapped real bug.
var eslintLevelSeverity = map[int]finding.Severity{
	2: finding.SeverityMedium,
	1: finding.SeverityLow,
}

// classifyESLintRule maps one ESLint result to a category and severity.
// level is ESLint's numeric severity (1 = warn, 2 = error). A ruleID of ""
// means a parse/config error rather than a rule violation — ESLint reports
// those as fatal messages, and they're correctness problems of the highest
// order since nothing in that file got linted at all.
func classifyESLintRule(ruleID string, level int, fatal bool) (finding.Category, finding.Severity) {
	if fatal || ruleID == "" {
		return finding.CategoryCorrectness, finding.SeverityHigh
	}
	if c, ok := eslintRuleClasses[ruleID]; ok {
		return c.Category, c.Severity
	}
	category := finding.CategoryCorrectness
	for prefix, cat := range eslintPluginCategories {
		if strings.HasPrefix(ruleID, prefix) {
			category = cat
			break
		}
	}
	severity, ok := eslintLevelSeverity[level]
	if !ok {
		severity = finding.SeverityLow
	}
	return category, severity
}
