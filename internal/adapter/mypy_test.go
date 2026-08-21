package adapter

import (
	"regexp"
	"strings"
	"testing"

	"codebase-analyser/internal/finding"
)

const mypySample = `bad.py:10: error: Incompatible return value type (got "str", expected "int")  [return-value]
bad.py:10: note: Revealed type is "builtins.str"
bad.py:15: error: Argument 1 to "greet" has incompatible type "int"; expected "str"  [arg-type]
`

func TestMypyFindings(t *testing.T) {
	findings := mypyFindings(mypySample)
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2 (the note: line must be skipped): %+v", len(findings), findings)
	}

	if findings[0].File != "bad.py" || findings[0].Line != 10 || findings[0].RuleID != "return-value" {
		t.Errorf("findings[0] = %+v, want File=bad.py Line=10 RuleID=return-value", findings[0])
	}
	if findings[0].Category != finding.CategoryCorrectness || findings[0].Severity != finding.SeverityHigh {
		t.Errorf("findings[0] class = (%v, %v), want (correctness, high)", findings[0].Category, findings[0].Severity)
	}

	if findings[1].RuleID != "arg-type" || findings[1].Line != 15 {
		t.Errorf("findings[1] = %+v, want Line=15 RuleID=arg-type", findings[1])
	}
}

func TestMypyFindings_cleanRunProducesNoFindings(t *testing.T) {
	if findings := mypyFindings(""); len(findings) != 0 {
		t.Errorf("mypyFindings(\"\") = %+v, want empty", findings)
	}
	if findings := mypyFindings("Success: no issues found in 3 source files\n"); len(findings) != 0 {
		t.Errorf("mypyFindings(summary-only) = %+v, want empty", findings)
	}
}

// A project-level diagnostic names a file but carries no line number, so
// mypyDiagLine never matches it. Before mypyProjectDiagLine it was dropped
// silently and a broken project reported clean.
func TestMypyFindings_filelessProjectDiagnosticStillSurfaces(t *testing.T) {
	const out = `pkg/__init__.py: error: Duplicate module named "pkg" (also at "./build/lib/pkg/__init__.py")
pkg/__init__.py: note: Are you missing an __init__.py?

`
	findings := mypyFindings(out)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1 (the note: and blank lines must be skipped): %+v", len(findings), findings)
	}
	f := findings[0]
	if f.File != "pkg/__init__.py" || f.Line != 0 || f.RuleID != "" {
		t.Errorf("finding = %+v, want File=pkg/__init__.py Line=0 RuleID=\"\"", f)
	}
	if f.Category != finding.CategoryCorrectness || f.Severity != finding.SeverityHigh {
		t.Errorf("class = (%v, %v), want (correctness, high)", f.Category, f.Severity)
	}
	if !strings.HasPrefix(f.Message, "Duplicate module") {
		t.Errorf("Message = %q, want the diagnostic text", f.Message)
	}
}

func TestMypyPythonVersionArgs(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		want []string
	}{
		{"absent leaves the invocation untouched", []string{"PATH=/usr/bin"}, nil},
		{"nil env", nil, nil},
		{"patch component is truncated", []string{"CODEBASE_ANALYSER_PYTHON_VERSION=3.11.4"}, []string{"--python-version", "3.11"}},
		{"already major.minor", []string{"CODEBASE_ANALYSER_PYTHON_VERSION=3.12"}, []string{"--python-version", "3.12"}},
		{"pyenv virtualenv name is dropped, not passed to mypy", []string{"CODEBASE_ANALYSER_PYTHON_VERSION=myproj-3.11"}, nil},
		{"non-CPython build is dropped", []string{"CODEBASE_ANALYSER_PYTHON_VERSION=pypy3.10-7.3.15"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mypyPythonVersionArgs(tc.env)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("mypyPythonVersionArgs(%v) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

// The exclude flag is a regex (mypy's own contract), not ruff's glob: check
// the built alternation actually compiles and matches the trees it must.
func TestMypyExcludeRegex(t *testing.T) {
	re := regexp.MustCompile(mypyExcludeRegex)
	for _, path := range []string{"build/lib/pkg/__init__.py", ".venv/lib/x.py", "src/__pycache__/a.py", "mypkg.egg-info/PKG-INFO", "dist"} {
		if !re.MatchString(path) {
			t.Errorf("%q not excluded by %q", path, mypyExcludeRegex)
		}
	}
	for _, path := range []string{"src/app.py", "rebuild/app.py", "distributed/app.py"} {
		if re.MatchString(path) {
			t.Errorf("%q wrongly excluded by %q", path, mypyExcludeRegex)
		}
	}
}
