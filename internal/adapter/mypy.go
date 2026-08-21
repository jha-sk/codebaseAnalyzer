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
	args := []string{"--show-error-codes", "--no-error-summary", "--no-color-output", "--exclude", mypyExcludeRegex}
	args = append(args, mypyPythonVersionArgs(EnvForPath(path))...)
	args = append(args, ".")
	out, err := runCommand(path, bin, args...)
	if err != nil {
		return nil, fmt.Errorf("mypy: %w", err)
	}
	return mypyFindings(string(out)), nil
}

// mypyExcludeRegex keeps mypy out of the generated/vendored trees in
// pyExcludedDirs. Unlike ruff's globs this is a REGEX - that is mypy's own
// --exclude contract - and it is one combined alternation passed once rather
// than a repeated flag, because only mypy >= 0.981 ORs repeated --exclude
// flags together.
var mypyExcludeRegex = buildMypyExcludeRegex()

func buildMypyExcludeRegex() string {
	parts := make([]string, 0, len(pyExcludedDirs)+1)
	for _, dir := range pyExcludedDirs {
		parts = append(parts, regexp.QuoteMeta(dir))
	}
	// *.egg-info is a suffix pattern, not a fixed name (mypkg.egg-info).
	parts = append(parts, `[^/]+\.egg-info`)
	return `(^|/)(` + strings.Join(parts, "|") + `)(/|$)`
}

// mypyPythonVersionArgs turns the interpreter version the toolchain resolved
// for this repo into mypy's --python-version flag. toolchain.Python.Ensure
// plants it in the tool environment as CODEBASE_ANALYSER_PYTHON_VERSION, so
// this adapter reads it straight off EnvForPath - the same "an adapter reads
// what it needs directly" pattern eslint.go's Run uses when it shells out for
// its own --version probe, rather than new plumbing through the orchestrator.
// Absent (the plain CLI leaves EnvForPath nil-returning) means no flag, i.e.
// exactly the pre-existing invocation.
var majorMinor = regexp.MustCompile(`^\d+\.\d+$`)

func mypyPythonVersionArgs(env []string) []string {
	const prefix = "CODEBASE_ANALYSER_PYTHON_VERSION="
	for _, e := range env {
		v, ok := strings.CutPrefix(e, prefix)
		if !ok || v == "" {
			continue
		}
		// mypy's --python-version rejects a patch component: 3.11.4 -> 3.11.
		if parts := strings.SplitN(v, ".", 3); len(parts) == 3 {
			v = parts[0] + "." + parts[1]
		}
		// A .python-version file may name a pyenv virtualenv or a non-CPython
		// build ("myproj-3.11", "pypy3.10-7.3.15"). Passing that through makes
		// mypy exit 2 with no stdout, i.e. the whole type-check is lost - drop
		// the flag instead, which is exactly the pre-marker behaviour.
		if !majorMinor.MatchString(v) {
			return nil
		}
		return []string{"--python-version", v}
	}
	return nil
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

// mypyProjectDiagLine matches a project-level mypy diagnostic that still
// names a file but carries no line number, e.g.:
//
//	pkg/__init__.py: error: Duplicate module named "pkg" (also at "./build/lib/pkg/__init__.py")
//
// mypyDiagLine requires ":(\d+):" and never matches this shape, so without
// this second regex such a diagnostic is silently dropped - exactly the
// failure mode tsc.go's tscProjectDiagLine guards against for the same
// reason (a project-level problem must still surface as a finding, not
// report as a clean run). Only tried AFTER mypyDiagLine fails, since its
// greedy (.+) would otherwise swallow a normal "file:line:" prefix too;
// "note:" is excluded from the alternation for the same reason as above.
var mypyProjectDiagLine = regexp.MustCompile(`^(.+): (error|warning): (.*)$`)

// mypyFindings parses the plain-text output of
// `mypy --show-error-codes --no-error-summary --no-color-output`. Lines
// matching neither an error nor a warning diagnostic (notes, blank lines,
// a stray summary line) are skipped silently.
func mypyFindings(out string) []finding.Finding {
	var findings []finding.Finding
	for _, line := range strings.Split(out, "\n") {
		if m := mypyDiagLine.FindStringSubmatch(line); m != nil {
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
			continue
		}
		// A project-level diagnostic (a duplicate module, a broken config)
		// names a file but no line. It still becomes a finding at line 0, so
		// a broken project shows up instead of reporting clean. tscSeverity
		// is reused verbatim: error -> high, warning -> low is the identical
		// mapping, not worth a second copy under a mypy-specific name.
		if m := mypyProjectDiagLine.FindStringSubmatch(line); m != nil {
			findings = append(findings, finding.Finding{
				File:     m[1],
				Line:     0,
				Tool:     "mypy",
				Category: finding.CategoryCorrectness,
				Severity: tscSeverity(m[2]),
				Message:  m[3],
			})
		}
	}
	return findings
}
