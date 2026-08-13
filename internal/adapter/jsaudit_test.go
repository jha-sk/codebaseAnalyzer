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

// TestNpmAuditFindings_realCapture parses real `npm audit --json` output
// (npm 11.19.0, captured via Task 9 from a throwaway project depending on
// minimist@0.0.8, see
// .superpowers/sdd/2026-08-13-jsts-language-support/real-tool-output/npm-audit.json).
// It exists to prove the parser against bytes npm actually emitted, not just
// a hand-authored fixture. Unlike testdata/npmaudit_sample.json, real npm
// here reported the package's "via" as TWO advisory objects (this version
// range is covered by two separate GHSA ids at different severities) rather
// than one object plus one bare-string transitive entry, so this also covers
// the multi-advisory-per-package case the hand-authored fixture doesn't.
func TestNpmAuditFindings_realCapture(t *testing.T) {
	raw, err := os.ReadFile("testdata/npmaudit_real_sample.json")
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
	wantIDs := map[string]finding.Severity{
		"GHSA-vh95-rmgr-6w4m": finding.SeverityMedium,
		"GHSA-xvch-5gv4-984h": finding.SeverityCritical,
	}
	for _, f := range findings {
		want, ok := wantIDs[f.RuleID]
		if !ok {
			t.Errorf("unexpected RuleID %q: %+v", f.RuleID, f)
			continue
		}
		if f.Severity != want || f.Category != finding.CategorySecurity {
			t.Errorf("finding %q severity/category = %v/%v, want %v/security", f.RuleID, f.Severity, f.Category, want)
		}
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

// TestNpmAuditFindings_networkError is Critical 1's regression test: npm
// audit failing at the network layer (offline CI, a proxy, ENEEDAUTH, an
// auth-failed private registry, ...) writes an error payload with NO
// "vulnerabilities" key to stdout and exits non-zero. runCommand already
// treats a non-zero exit WITH stdout as success, so this payload reaches
// npmAuditFindings looking almost like real output - the only signal that
// it isn't is the missing key. That must produce an error naming the real
// reason (from error.summary), never (nil, nil), or the report claims a
// broken audit was a clean one.
func TestNpmAuditFindings_networkError(t *testing.T) {
	raw, err := os.ReadFile("testdata/npmaudit_error_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := npmAuditFindings(raw)
	if err == nil {
		t.Fatalf("npmAuditFindings: got nil error, want one naming the failure (findings: %+v)", findings)
	}
	if !strings.Contains(err.Error(), "ECONNREFUSED") {
		t.Errorf("error = %q, want it to name the underlying reason (ECONNREFUSED)", err.Error())
	}
	if findings != nil {
		t.Errorf("findings = %+v, want nil alongside the error", findings)
	}
}

// TestNpmAuditFindings_viaAllUnrecognisedShape is the deferred minor folded
// into Critical 1: a package whose every "via" entry is neither a bare
// string nor an advisory object (some shape this parser doesn't know) must
// still produce one finding, using the package-level severity - not vanish
// silently the way it did before.
func TestNpmAuditFindings_viaAllUnrecognisedShape(t *testing.T) {
	findings, err := npmAuditFindings([]byte(`{"vulnerabilities":{"weird-pkg":{"name":"weird-pkg","severity":"high","via":[42, true]}}}`))
	if err != nil {
		t.Fatalf("npmAuditFindings: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Severity != finding.SeverityHigh {
		t.Errorf("Severity = %v, want high (from the package-level severity)", findings[0].Severity)
	}
	if !strings.Contains(findings[0].Message, "weird-pkg") {
		t.Errorf("Message = %q, want it to name weird-pkg", findings[0].Message)
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

// TestYarnAuditFindings_errorLine is Critical 1's yarn-side regression test:
// a stream consisting of a single {"type":"error",...} line (yarn's shape
// for a real failure, e.g. registry unreachable) previously decoded to
// nothing (the Data struct expected an object, got a string, so the whole
// line failed json.Unmarshal and was skipped) and yielded (nil, nil) - a
// broken audit read as clean. It must now surface as a real error naming
// yarn's own message.
func TestYarnAuditFindings_errorLine(t *testing.T) {
	raw, err := os.ReadFile("testdata/yarnaudit_error_sample.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := yarnAuditFindings(bytes.NewReader(raw))
	if err == nil {
		t.Fatalf("yarnAuditFindings: got nil error, want one naming the failure (findings: %+v)", findings)
	}
	if !strings.Contains(err.Error(), "ECONNREFUSED") {
		t.Errorf("error = %q, want it to name the underlying reason (ECONNREFUSED)", err.Error())
	}
}

// TestYarnAuditFindings_noRecognisedLines covers the stream never containing
// a single line yarn audit actually emits (garbage output, an empty
// response, ...): no advisories AND no recognisable audit line at all. That
// must not read as "zero vulnerabilities found" - a genuinely clean audit
// still emits at least an auditSummary line.
func TestYarnAuditFindings_noRecognisedLines(t *testing.T) {
	findings, err := yarnAuditFindings(strings.NewReader("not json at all\nneither is this\n"))
	if err == nil {
		t.Fatalf("yarnAuditFindings: got nil error, want one (findings: %+v)", findings)
	}
	if findings != nil {
		t.Errorf("findings = %+v, want nil alongside the error", findings)
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

// TestJSAudit_Run_lockfileAtWorkspaceRoot covers the workspace-member case:
// no lockfile in this project dir itself, but one exists at an ancestor
// (the repo root). That must stay silent (nil, nil) - the root project is
// detected separately and audits the shared lockfile there exactly once.
func TestJSAudit_Run_lockfileAtWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "package-lock.json"), "{}")
	member := filepath.Join(root, "packages", "a")
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatal(err)
	}

	findings, err := JSAudit{}.Run(member)
	if findings != nil {
		t.Errorf("findings = %+v, want nil", findings)
	}
	if err != nil {
		t.Errorf("err = %v, want nil: the root's lockfile covers this member", err)
	}
}

// TestJSAudit_Run_noLockfileAnywhere is Important 4's regression test: a
// real lockfile-less repo with actual dependencies to audit (no lockfile in
// this dir OR any ancestor, i.e. this isn't a workspace member either) must
// not silently report as "nothing to audit" - it must surface as an error so
// the orchestrator reports js-audit as a skipped tool with a reason.
func TestJSAudit_Run_noLockfileAnywhere(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "package.json"), `{"name":"x","dependencies":{"left-pad":"1.0.0"}}`)

	findings, err := JSAudit{}.Run(dir)
	if findings != nil {
		t.Errorf("findings = %+v, want nil", findings)
	}
	if err == nil {
		t.Fatal("err = nil, want an error: no lockfile anywhere up the tree, and the package declares dependencies, so this repo is genuinely unaudited")
	}
}

// TestJSAudit_Run_noDependenciesNoLockfile is the regression test for the
// fix to Important 4: a package with NO declared dependencies and no
// lockfile anywhere has nothing to audit, so it must report (nil, nil), not
// an error. Before this fix every dependency-less JS/TS package (and every
// repo that gitignores its lockfile) turned a clean run into exit code 2
// ("analysis incomplete") purely because of the missing lockfile, regardless
// of whether there was anything to audit at all.
func TestJSAudit_Run_noDependenciesNoLockfile(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "package.json"), `{"name":"x"}`)

	findings, err := JSAudit{}.Run(dir)
	if findings != nil {
		t.Errorf("findings = %+v, want nil", findings)
	}
	if err != nil {
		t.Errorf("err = %v, want nil: no declared dependencies means there is nothing to audit", err)
	}
}

// TestJSAudit_Run_emptyDependenciesNoLockfile covers a package.json that
// declares "dependencies"/"devDependencies" but as empty objects - the same
// as declaring none at all, so this must also be (nil, nil), not an error.
func TestJSAudit_Run_emptyDependenciesNoLockfile(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "package.json"), `{"name":"x","dependencies":{},"devDependencies":{}}`)

	findings, err := JSAudit{}.Run(dir)
	if findings != nil {
		t.Errorf("findings = %+v, want nil", findings)
	}
	if err != nil {
		t.Errorf("err = %v, want nil: empty dependency objects mean there is nothing to audit", err)
	}
}

// TestLockfileExistsAbove_boundedAtGit is fix B's regression test: the walk
// must stop at the directory containing .git (or the filesystem root),
// never wander further up into a directory outside the repo. Without the
// bound, a lockfile that happens to sit above the repo root (a stray
// package-lock.json in $HOME, /tmp or /) would silently make every real
// lockfile-less repo look like a covered workspace member.
func TestLockfileExistsAbove_boundedAtGit(t *testing.T) {
	outer := t.TempDir()
	mustWriteFile(t, filepath.Join(outer, "package-lock.json"), "{}")

	repo := filepath.Join(outer, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	member := filepath.Join(repo, "packages", "a")
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatal(err)
	}

	if lockfileExistsAbove(member) {
		t.Error("lockfileExistsAbove(member) = true, want false: the lockfile sits above the .git boundary and must not be found")
	}
}
