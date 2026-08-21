package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestDetectPipManifest(t *testing.T) {
	tests := []struct {
		name         string
		files        []string
		wantManifest string
		wantDashR    bool
	}{
		{"requirements.txt only", []string{"requirements.txt"}, "requirements.txt", true},
		{"poetry.lock only", []string{"poetry.lock"}, "poetry.lock", false},
		{"uv.lock only", []string{"uv.lock"}, "uv.lock", false},
		{"requirements.txt wins over poetry.lock", []string{"requirements.txt", "poetry.lock"}, "requirements.txt", true},
		{"none present", nil, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				os.WriteFile(filepath.Join(dir, f), []byte(""), 0o644)
			}
			manifest, dashR := detectPipManifest(dir)
			if manifest != tc.wantManifest || dashR != tc.wantDashR {
				t.Errorf("detectPipManifest = (%q, %v), want (%q, %v)", manifest, dashR, tc.wantManifest, tc.wantDashR)
			}
		})
	}
}

// TestPipAuditRun_pipenvOnlyReportsAClearSkip covers the spec's explicit
// case: a Pipfile.lock with none of the three supported manifests must
// surface as a named error (a skipped tool with a reason), never as a
// silent "nothing to audit".
func TestPipAuditRun_pipenvOnlyReportsAClearSkip(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Pipfile.lock"), []byte("{}"), 0o644)

	_, err := PipAudit{}.Run(dir)
	if err == nil {
		t.Fatal("err = nil, want an error naming the unsupported Pipenv-only manifest")
	}
	if got := err.Error(); !strings.Contains(got, "Pipenv") {
		t.Errorf("err = %q, want it to mention Pipenv", got)
	}
}

// TestPipAuditRun_noManifestAtAllIsHealthySilence covers a project with
// genuinely nothing to audit: no supported manifest and no Pipfile.lock
// either. This must not be reported as an error.
func TestPipAuditRun_noManifestAtAllIsHealthySilence(t *testing.T) {
	dir := t.TempDir()
	findings, err := PipAudit{}.Run(dir)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil for a directory with nothing to audit", err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none", findings)
	}
}

func TestPipAuditFindings(t *testing.T) {
	raw, err := os.ReadFile("testdata/pipaudit_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := pipAuditFindings(raw)
	if err != nil {
		t.Fatalf("pipAuditFindings: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1 (flask has no vulns): %+v", len(findings), findings)
	}
	f := findings[0]
	if f.RuleID != "PYSEC-2018-28" {
		t.Errorf("RuleID = %q, want PYSEC-2018-28", f.RuleID)
	}
	if f.Category != finding.CategorySecurity || f.Severity != finding.SeverityHigh {
		t.Errorf("class = (%v, %v), want (security, high)", f.Category, f.Severity)
	}
	if f.Tool != "pip-audit" {
		t.Errorf("Tool = %q, want pip-audit", f.Tool)
	}
}
