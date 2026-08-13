package adapter

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestNpmAuditFindings(t *testing.T) {
	raw, err := os.ReadFile("testdata/npmaudit_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := npmAuditFindings(raw)
	if err != nil {
		t.Fatalf("npmAuditFindings: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}

	var advisory, transitive *finding.Finding
	for i := range findings {
		f := &findings[i]
		if f.RuleID != "" {
			advisory = f
		} else {
			transitive = f
		}
	}
	if advisory == nil {
		t.Fatalf("no finding carries an advisory RuleID: %+v", findings)
	}
	if advisory.RuleID != "GHSA-xvch-5gv4-984h" {
		t.Errorf("advisory RuleID = %q, want GHSA-xvch-5gv4-984h", advisory.RuleID)
	}
	if advisory.Severity != finding.SeverityCritical {
		t.Errorf("advisory Severity = %v, want critical", advisory.Severity)
	}
	if advisory.Category != finding.CategorySecurity {
		t.Errorf("advisory Category = %v, want security", advisory.Category)
	}

	if transitive == nil {
		t.Fatalf("no finding for the string-via (transitive) package: %+v", findings)
	}
	if transitive.Severity != finding.SeverityMedium {
		t.Errorf("transitive Severity = %v, want medium (from moderate)", transitive.Severity)
	}
	if !strings.Contains(transitive.Message, "tough-cookie") {
		t.Errorf("transitive Message = %q, want it to name tough-cookie", transitive.Message)
	}
}

func TestNpmAuditFindings_empty(t *testing.T) {
	findings, err := npmAuditFindings([]byte(`{"vulnerabilities":{}}`))
	if err != nil {
		t.Fatalf("npmAuditFindings: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("got %d findings, want 0: %+v", len(findings), findings)
	}
}

func TestYarnAuditFindings(t *testing.T) {
	raw, err := os.ReadFile("testdata/yarnaudit_sample.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := yarnAuditFindings(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("yarnAuditFindings: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1 (auditSummary line must contribute nothing): %+v", len(findings), findings)
	}
	if findings[0].RuleID != "GHSA-xvch-5gv4-984h" {
		t.Errorf("RuleID = %q, want GHSA-xvch-5gv4-984h", findings[0].RuleID)
	}
	if findings[0].Severity != finding.SeverityCritical {
		t.Errorf("Severity = %v, want critical", findings[0].Severity)
	}
}

func TestYarnAuditFindings_toleratesGarbageLines(t *testing.T) {
	input := `{"type":"auditAdvisory","data":{"resolution":{"id":1179,"path":"minimist"},"advisory":{"module_name":"minimist","severity":"critical","title":"Prototype Pollution","github_advisory_id":"GHSA-xvch-5gv4-984h","cwe":"CWE-1321"}}}
not valid json at all

`
	findings, err := yarnAuditFindings(strings.NewReader(input))
	if err != nil {
		t.Fatalf("yarnAuditFindings: %v (a non-JSON line and a trailing blank line must not error the parse)", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
}

func TestPnpmAuditFindings(t *testing.T) {
	raw, err := os.ReadFile("testdata/pnpmaudit_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := pnpmAuditFindings(raw)
	if err != nil {
		t.Fatalf("pnpmAuditFindings: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].RuleID != "GHSA-xvch-5gv4-984h" {
		t.Errorf("RuleID = %q, want GHSA-xvch-5gv4-984h", findings[0].RuleID)
	}
	if findings[0].Severity != finding.SeverityCritical {
		t.Errorf("Severity = %v, want critical", findings[0].Severity)
	}
}

func TestPnpmAuditFindings_sortedDeterministic(t *testing.T) {
	raw, err := os.ReadFile("testdata/pnpmaudit_two_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		findings, err := pnpmAuditFindings(raw)
		if err != nil {
			t.Fatalf("pnpmAuditFindings: %v", err)
		}
		if len(findings) != 2 {
			t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
		}
		if findings[0].RuleID != "GHSA-xvch-5gv4-984h" || findings[1].RuleID != "GHSA-72xf-g2v4-qvf3" {
			t.Fatalf("run %d: order = [%s, %s], want stable [GHSA-xvch-5gv4-984h, GHSA-72xf-g2v4-qvf3]", i, findings[0].RuleID, findings[1].RuleID)
		}
	}
}

// TestPnpmAuditFindings_modernShape covers pnpm versions that report through
// the npm-v7-style backend and therefore emit a "vulnerabilities" map
// instead of the legacy "advisories" map — reusing the npm fixture directly,
// since that IS the shape this falls back to.
func TestPnpmAuditFindings_modernShape(t *testing.T) {
	raw, err := os.ReadFile("testdata/npmaudit_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := pnpmAuditFindings(raw)
	if err != nil {
		t.Fatalf("pnpmAuditFindings: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}
}

// TestPnpmAuditFindings_unrecognisedShape asserts that a payload with
// neither "advisories" nor "vulnerabilities" at all is reported loudly as an
// error, not silently as zero findings. json.Unmarshal would otherwise leave
// both maps at their nil zero value indistinguishably from a genuinely clean
// report, which is the exact silent-zero-findings failure mode this guards
// against.
func TestPnpmAuditFindings_unrecognisedShape(t *testing.T) {
	findings, err := pnpmAuditFindings([]byte(`{"foo":"bar"}`))
	if err == nil {
		t.Fatalf("pnpmAuditFindings: got nil error, want an error naming the unrecognised shape (findings: %+v)", findings)
	}
	if findings != nil {
		t.Errorf("findings = %+v, want nil alongside the error", findings)
	}
}

func TestJSSeverity(t *testing.T) {
	cases := []struct {
		in   string
		want finding.Severity
	}{
		{"critical", finding.SeverityCritical},
		{"high", finding.SeverityHigh},
		{"moderate", finding.SeverityMedium},
		{"low", finding.SeverityLow},
		{"info", finding.SeverityLow},
		{"unknown-thing", finding.SeverityMedium},
	}
	for _, c := range cases {
		if got := jsSeverity(c.in); got != c.want {
			t.Errorf("jsSeverity(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectLockfile(t *testing.T) {
	t.Run("npm package-lock.json", func(t *testing.T) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "package-lock.json"), "{}")
		lockfile, manager := detectLockfile(dir)
		if lockfile != "package-lock.json" || manager != "npm" {
			t.Errorf("got (%q, %q), want (package-lock.json, npm)", lockfile, manager)
		}
	})
	t.Run("npm npm-shrinkwrap.json", func(t *testing.T) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "npm-shrinkwrap.json"), "{}")
		lockfile, manager := detectLockfile(dir)
		if lockfile != "npm-shrinkwrap.json" || manager != "npm" {
			t.Errorf("got (%q, %q), want (npm-shrinkwrap.json, npm)", lockfile, manager)
		}
	})
	t.Run("yarn yarn.lock", func(t *testing.T) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "yarn.lock"), "")
		lockfile, manager := detectLockfile(dir)
		if lockfile != "yarn.lock" || manager != "yarn" {
			t.Errorf("got (%q, %q), want (yarn.lock, yarn)", lockfile, manager)
		}
	})
	t.Run("pnpm pnpm-lock.yaml", func(t *testing.T) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "pnpm-lock.yaml"), "")
		lockfile, manager := detectLockfile(dir)
		if lockfile != "pnpm-lock.yaml" || manager != "pnpm" {
			t.Errorf("got (%q, %q), want (pnpm-lock.yaml, pnpm)", lockfile, manager)
		}
	})
	t.Run("npm wins over yarn when both lockfiles exist", func(t *testing.T) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "package-lock.json"), "{}")
		mustWriteFile(t, filepath.Join(dir, "yarn.lock"), "")
		lockfile, manager := detectLockfile(dir)
		if lockfile != "package-lock.json" || manager != "npm" {
			t.Errorf("got (%q, %q), want (package-lock.json, npm)", lockfile, manager)
		}
	})
	t.Run("no lockfile", func(t *testing.T) {
		lockfile, manager := detectLockfile(t.TempDir())
		if lockfile != "" || manager != "" {
			t.Errorf("got (%q, %q), want (\"\", \"\")", lockfile, manager)
		}
	})
}

func TestJSAudit_Run_noLockfile(t *testing.T) {
	findings, err := JSAudit{}.Run(t.TempDir())
	if findings != nil {
		t.Errorf("findings = %+v, want nil", findings)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}
