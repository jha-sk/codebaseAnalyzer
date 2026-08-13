package adapter

import (
	"testing"

	"codebase-analyser/internal/finding"
)

func TestTscFindings(t *testing.T) {
	out := `src/index.ts(12,7): error TS2322: Type 'string' is not assignable to type 'number'.
src/api/client.ts(48,15): error TS2532: Object is possibly 'undefined'.

Found 2 errors in 2 files.
error TS18003: No inputs were found in config file '/repo/tsconfig.json'.
`
	findings := tscFindings(out)
	if len(findings) != 3 {
		t.Fatalf("got %d findings, want 3: %+v", len(findings), findings)
	}

	f0 := findings[0]
	if f0.File != "src/index.ts" || f0.Line != 12 || f0.Tool != "tsc" || f0.RuleID != "TS2322" ||
		f0.Category != finding.CategoryCorrectness || f0.Severity != finding.SeverityHigh ||
		f0.Message != "Type 'string' is not assignable to type 'number'." {
		t.Errorf("findings[0] = %+v", f0)
	}

	f1 := findings[1]
	if f1.File != "src/api/client.ts" || f1.Line != 48 || f1.Tool != "tsc" || f1.RuleID != "TS2532" ||
		f1.Category != finding.CategoryCorrectness || f1.Severity != finding.SeverityHigh ||
		f1.Message != "Object is possibly 'undefined'." {
		t.Errorf("findings[1] = %+v", f1)
	}

	// TS18003 has no file prefix - it must still surface as a finding
	// (attributed to tsconfig.json) rather than being silently dropped,
	// otherwise a broken project configuration reports as clean.
	f2 := findings[2]
	if f2.File != "tsconfig.json" || f2.Line != 0 || f2.Tool != "tsc" || f2.RuleID != "TS18003" ||
		f2.Category != finding.CategoryCorrectness || f2.Severity != finding.SeverityHigh ||
		f2.Message != "No inputs were found in config file '/repo/tsconfig.json'." {
		t.Errorf("findings[2] = %+v", f2)
	}

	// "Found 2 errors in 2 files." and the blank line above must both be
	// skipped silently, not miscounted as findings or cause a panic.
}

func TestTscFindings_WindowsPath(t *testing.T) {
	// The naive regex `(.+)\((\d+),(\d+)\)` must not choke on the colon in
	// the "C:" drive letter - there is nothing colon-based in the regex to
	// confuse, since (.+) matches up to the LAST "(line,col)" on the line.
	out := `C:\repo\src\a.ts(3,1): error TS1005: ';' expected.`
	findings := tscFindings(out)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.File != `C:\repo\src\a.ts` || f.Line != 3 || f.RuleID != "TS1005" || f.Message != "';' expected." {
		t.Errorf("finding = %+v", f)
	}
}

func TestTscFindings_Empty(t *testing.T) {
	if findings := tscFindings(""); findings != nil {
		t.Errorf("tscFindings(\"\") = %+v, want nil", findings)
	}
}

func TestTscFindings_MessageSeverity(t *testing.T) {
	out := `message TS6194: Found 1 error.`
	findings := tscFindings(out)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Severity != finding.SeverityLow {
		t.Errorf("Severity = %v, want low", findings[0].Severity)
	}
}

func TestTscRun_NoTsconfig(t *testing.T) {
	// No tsconfig.json in the temp dir means there is nothing for tsc to
	// type-check. This must return (nil, nil), NOT an error - an error here
	// would make the orchestrator report tsc as a skipped tool and degrade
	// the run's exit code to 2 for a project that was never TypeScript.
	findings, err := (Tsc{}).Run(t.TempDir())
	if findings != nil {
		t.Errorf("findings = %+v, want nil", findings)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}
