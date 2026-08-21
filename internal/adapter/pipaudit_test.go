package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codebase-analyser/internal/finding"
)

// TestPipAuditRun_unsupportedLockfilesReportAClearSkip covers the spec's
// "skipped tool, with a reason" contract for every dependency manifest
// pip-audit cannot read in v1 (the Pipenv case has its own test below).
// poetry.lock/uv.lock used to be dispatched to
// a real pip-audit invocation with no -r flag, which audits the analyser's
// OWN venv rather than the repo - a false negative and a false positive at
// once. They must be named skips instead.
func TestPipAuditRun_unsupportedLockfilesReportAClearSkip(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"poetry.lock", "poetry.lock"},
		{"uv.lock", "uv.lock"},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, tc.file), []byte(""), 0o644)

			_, err := PipAudit{}.Run(dir)
			if err == nil {
				t.Fatalf("err = nil, want an error naming the unsupported %s", tc.file)
			}
			if got := err.Error(); !strings.Contains(got, tc.want) {
				t.Errorf("err = %q, want it to mention %q", got, tc.want)
			}
		})
	}
}

// A requirements.txt alongside an unsupported lockfile is still scanned: the
// lockfile skips only apply when there is no requirements.txt to read.
func TestPipAuditRun_requirementsTxtWinsOverAnUnsupportedLockfile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "poetry.lock"), []byte(""), 0o644)

	_, err := PipAudit{}.Run(dir)
	if err != nil && strings.Contains(err.Error(), "poetry.lock") {
		t.Errorf("err = %q, want no poetry.lock skip when requirements.txt is present", err)
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

// pip-audit gets exactly one invocation shape, and every flag in it is
// load-bearing: -r reads the repo's manifest (without it pip-audit audits the
// analyser's own venv), --no-deps + --disable-pip together stop it from
// pip-installing - and so executing - the analysed repo's dependency code.
func TestPipAuditArgs(t *testing.T) {
	want := []string{"-r", "requirements.txt", "--no-deps", "--disable-pip", "--format", "json"}
	got := pipAuditArgs("requirements.txt")
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("pipAuditArgs() = %v, want %v", got, want)
	}
}

// --disable-pip's price: every requirement must be pinned with ==. pip-audit
// hard-fails on an unpinned line, which must surface as the same named,
// actionable skip as an unsupported lockfile - not a generic "exited 1".
func TestPipAuditRun_unpinnedRequirementReportsAClearSkip(t *testing.T) {
	if !(PipAudit{}).CheckInstalled() {
		t.Skip("pip-audit not installed; this test needs the real binary's unpinned-requirement error")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("requests\n"), 0o644)

	_, err := PipAudit{}.Run(dir)
	if err == nil {
		t.Fatal("err = nil, want an error naming the unpinned requirement")
	}
	if got := err.Error(); !strings.Contains(got, "unpinned requirement") || !strings.Contains(got, "==") {
		t.Errorf("err = %q, want the actionable 'pin every line with ==' skip", got)
	}
}
