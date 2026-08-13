package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"codebase-analyser/internal/finding"
)

// Tsc wraps `tsc --noEmit`, the TypeScript compiler running in
// type-check-only mode. It shares the pinned Node/TypeScript install with
// the other JS/TS adapters (see js.go).
type Tsc struct{}

func (Tsc) Name() string         { return "tsc" }
func (Tsc) CheckInstalled() bool { return isExecutable(pinnedJSBin("tsc")) }
func (Tsc) Install() error       { return installJSTools() }

// Run type-checks the whole program rooted at path.
//
// Targeted is deliberately not implemented on Tsc: tsc type-checks a whole
// program (it follows imports wherever they lead) and has no meaningful
// notion of "just this subdirectory" to restrict a run to.
func (Tsc) Run(path string) ([]finding.Finding, error) {
	// tsc is only meaningful against a tsconfig.json - without one there is
	// nothing to type-check, and that is not an error: returning an error
	// here would make the orchestrator report tsc as a *skipped* tool and
	// degrade the run's exit code to 2, when "this isn't a TypeScript
	// project" is a perfectly healthy, non-error outcome.
	//
	// That reasoning only applies to a CONFIRMED absence, though. Any other
	// stat error - permission denied being the practical case - is not "no
	// tsconfig.json here"; it's "there might well be one and we can't tell",
	// which must surface as a real error (and so a skipped tool with a
	// reason) rather than silently skip the type-check.
	if _, err := os.Stat(filepath.Join(path, "tsconfig.json")); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("tsc: stat tsconfig.json: %w", err)
	}
	bin := jsBin(path, "tsc")
	if bin == "" {
		return nil, fmt.Errorf("tsc: not available")
	}
	// tsc has no JSON output mode, so the plain diagnostic format parsed by
	// tscFindings below is all that's available. --pretty false strips the
	// ANSI colour codes and multi-line framing tsc's default "pretty" output
	// uses, which would otherwise make each diagnostic unparseable as a
	// single line.
	//
	// tsc exits non-zero whenever it reports type errors; runCommand already
	// tolerates that (a non-zero exit WITH stdout is treated as success). A
	// tsconfig.json broken enough that tsc dies before emitting any
	// diagnostics produces a real runCommand error here, which is correct:
	// it should surface as a skipped tool with a reason, not silently as
	// zero findings.
	out, err := runCommand(path, bin, "--noEmit", "--pretty", "false")
	if err != nil {
		return nil, fmt.Errorf("tsc: %w", err)
	}
	return tscFindings(string(out)), nil
}

// tscFileDiagLine matches a file-scoped diagnostic, e.g.:
//
//	src/index.ts(12,7): error TS2322: Type 'string' is not assignable to type 'number'.
//	C:\repo\src\a.ts(3,1): error TS1005: ';' expected.
//
// (.+) is greedy, so it matches up to the LAST "(line,col)" on the line -
// that is what keeps a Windows drive-letter colon (C:\...) from being
// mistaken for a field separator: there is nothing colon-based in this
// regex to split on in the first place.
var tscFileDiagLine = regexp.MustCompile(`^(.+)\((\d+),(\d+)\): (error|message|suggestion) (TS\d+): (.*)$`)

// tscProjectDiagLine matches a project-level diagnostic with no file prefix,
// e.g.:
//
//	error TS18003: No inputs were found in config file '/repo/tsconfig.json'.
var tscProjectDiagLine = regexp.MustCompile(`^(error|message|suggestion) (TS\d+): (.*)$`)

// tscSeverity maps a tsc diagnostic level to a Finding severity. tsc has no
// "warning" level in this output, so none is invented here - only "error"
// (high) and "message"/"suggestion" (low) occur.
func tscSeverity(level string) finding.Severity {
	if level == "error" {
		return finding.SeverityHigh
	}
	return finding.SeverityLow
}

// tscFindings parses the plain-text output of `tsc --noEmit --pretty false`.
// tsc interleaves progress and summary lines with its diagnostics; any line
// matching neither diagnostic shape is skipped silently.
func tscFindings(out string) []finding.Finding {
	var findings []finding.Finding
	for _, line := range strings.Split(out, "\n") {
		if m := tscFileDiagLine.FindStringSubmatch(line); m != nil {
			lineNum, _ := strconv.Atoi(m[2])
			findings = append(findings, finding.Finding{
				File:     m[1],
				Line:     lineNum,
				Tool:     "tsc",
				RuleID:   m[5],
				Category: finding.CategoryCorrectness,
				Severity: tscSeverity(m[4]),
				Message:  m[6],
			})
			continue
		}
		// A project-level diagnostic (e.g. TS18003, a broken tsconfig.json)
		// has no file to attach to. It still becomes a finding - attributed
		// to "tsconfig.json" at line 0 - so a misconfigured project shows up
		// instead of silently reporting clean.
		if m := tscProjectDiagLine.FindStringSubmatch(line); m != nil {
			findings = append(findings, finding.Finding{
				File:     "tsconfig.json",
				Line:     0,
				Tool:     "tsc",
				RuleID:   m[2],
				Category: finding.CategoryCorrectness,
				Severity: tscSeverity(m[1]),
				Message:  m[3],
			})
		}
	}
	return findings
}
