package adapter

import (
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
