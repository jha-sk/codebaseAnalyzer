package adapter

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"codebase-analyser/internal/finding"
)

// Mypy wraps `mypy`. Not Targeted: mypy type-checks a whole program via
// imports, same reasoning as tsc.go - no meaningful "just this
// subdirectory" restriction.
type Mypy struct{}

func (Mypy) Name() string         { return "mypy" }
func (Mypy) CheckInstalled() bool { return isExecutable(pinnedPyBin("mypy")) }
func (Mypy) Install() error       { return installPyTools() }

func (Mypy) Run(path string) ([]finding.Finding, error) {
	bin := pinnedPyBin("mypy")
	// mypy has no dependable JSON output mode (unlike ruff's stable
	// --output-format json) - parsed as plain text via mypyDiagLine below,
	// the same choice tsc.go makes for the same reason. mypy exits non-zero
	// whenever it reports errors; runCommand already tolerates that (a
	// non-zero exit WITH stdout is treated as success).
	out, err := runCommand(path, bin, "--show-error-codes", "--no-error-summary", "--no-color-output", ".")
	if err != nil {
		return nil, fmt.Errorf("mypy: %w", err)
	}
	return mypyFindings(string(out)), nil
}

// mypyDiagLine matches mypy's default diagnostic line, e.g.:
//
//	src/app.py:10: error: Incompatible return value type  [return-value]
//
// "note:" lines are deliberately excluded from the alternation rather than
// matched-and-dropped: they almost always elaborate on the immediately
// preceding error/warning (e.g. "note: Revealed type is ...") rather than
// standing alone as an actionable finding, so they simply never match here.
// The trailing `[error-code]` is optional since not every mypy diagnostic
// carries one.
var mypyDiagLine = regexp.MustCompile(`^(.+):(\d+): (error|warning): (.*?)(?:\s+\[([a-z][a-z0-9-]*)\])?$`)

// mypyFindings parses the plain-text output of
// `mypy --show-error-codes --no-error-summary --no-color-output`. Lines
// matching neither an error nor a warning diagnostic (notes, blank lines,
// a stray summary line) are skipped silently.
func mypyFindings(out string) []finding.Finding {
	var findings []finding.Finding
	for _, line := range strings.Split(out, "\n") {
		m := mypyDiagLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNum, _ := strconv.Atoi(m[2])
		findings = append(findings, finding.Finding{
			File:     m[1],
			Line:     lineNum,
			Tool:     "mypy",
			RuleID:   m[5],
			Category: finding.CategoryCorrectness,
			Severity: classifyMypyCode(m[5]),
			Message:  m[4],
		})
	}
	return findings
}
