package adapter

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestJSToolsDirHonoursCacheOverride: tests must be able to redirect the
// tool cache, and CODEBASE_ANALYSER_CACHE is the seam cache.Root already
// uses for the same reason (os.UserCacheDir honours XDG_CACHE_HOME on Linux
// but has no equivalent on macOS or Windows).
func TestJSToolsDirHonoursCacheOverride(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", "/tmp/analyser-cache-test")
	if got, want := jsToolsDir(), filepath.Join("/tmp/analyser-cache-test", "js-tools"); got != want {
		t.Errorf("jsToolsDir() = %q, want %q", got, want)
	}
}

func TestFindUp(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "packages", "a", "src")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pnpm-lock.yaml"), []byte("lockfileVersion: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := findUp(deep, "pnpm-lock.yaml"); got != root {
		t.Errorf("findUp = %q, want %q", got, root)
	}
	if got := findUp(deep, "no-such-file.json"); got != "" {
		t.Errorf("findUp for a missing file = %q, want \"\" (it must stop at the filesystem root, not loop)", got)
	}
}

func TestIsExecutable(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.txt")
	os.WriteFile(plain, []byte("x"), 0o644)
	exe := filepath.Join(dir, "runme")
	os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755)

	if isExecutable(dir) {
		t.Error("isExecutable(dir) = true, want false")
	}
	if isExecutable(plain) {
		t.Error("isExecutable(non-executable file) = true, want false")
	}
	if !isExecutable(exe) {
		t.Error("isExecutable(executable file) = false, want true")
	}
	if isExecutable(filepath.Join(dir, "nope")) {
		t.Error("isExecutable(missing) = true, want false")
	}
}

// TestJSBinPrefersRepoLocal is the important one: a repo that ships its own
// ESLint must be linted with that copy, because its config and plugins only
// resolve there. The pinned copy is the fallback, not the default.
func TestJSBinPrefersRepoLocal(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("CODEBASE_ANALYSER_CACHE", cache)
	pinned := filepath.Join(cache, "js-tools", "node_modules", ".bin")
	os.MkdirAll(pinned, 0o755)
	os.WriteFile(filepath.Join(pinned, "eslint"), []byte("#!/bin/sh\n"), 0o755)

	repo := t.TempDir()
	member := filepath.Join(repo, "packages", "a")
	os.MkdirAll(member, 0o755)

	// No repo-local copy anywhere: falls back to the pinned one.
	if got, want := jsBin(member, "eslint"), filepath.Join(pinned, "eslint"); got != want {
		t.Errorf("jsBin with no repo-local copy = %q, want the pinned %q", got, want)
	}

	// A hoisted install at the repo root wins for a member package.
	hoisted := filepath.Join(repo, "node_modules", ".bin")
	os.MkdirAll(hoisted, 0o755)
	os.WriteFile(filepath.Join(hoisted, "eslint"), []byte("#!/bin/sh\n"), 0o755)
	if got, want := jsBin(member, "eslint"), filepath.Join(hoisted, "eslint"); got != want {
		t.Errorf("jsBin = %q, want the hoisted repo copy %q", got, want)
	}

	// A copy in the member package itself wins over the hoisted one.
	local := filepath.Join(member, "node_modules", ".bin")
	os.MkdirAll(local, 0o755)
	os.WriteFile(filepath.Join(local, "eslint"), []byte("#!/bin/sh\n"), 0o755)
	if got, want := jsBin(member, "eslint"), filepath.Join(local, "eslint"); got != want {
		t.Errorf("jsBin = %q, want the member-local copy %q", got, want)
	}
}

func TestJSBinMissingEverywhere(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	if got := jsBin(t.TempDir(), "eslint"); got != "" {
		t.Errorf("jsBin = %q, want \"\" when the tool exists nowhere", got)
	}
}

// TestPinnedPackagesAreVersionPinned guards the reproducibility promise: an
// unpinned entry silently reintroduces "results shift when a tool
// publishes", which is the whole reason this cache exists.
func TestPinnedPackagesAreVersionPinned(t *testing.T) {
	for _, p := range jsPinnedPackages {
		// Strip a leading scope so "@eslint/js@9.39.0" still has its version found.
		at := strings.LastIndex(p, "@")
		if at <= 0 || at == len(p)-1 {
			t.Errorf("package %q is not version-pinned", p)
		}
	}
}

// TestBaselineConfigsEmbedded: the go:embed directives are silent about an
// empty file, and an empty baseline would make every no-config repo report
// zero findings while looking like a clean pass.
func TestBaselineConfigsEmbedded(t *testing.T) {
	if !strings.Contains(string(baselineFlatConfig), "export default") {
		t.Error("flat baseline does not export a config")
	}
	var legacy map[string]any
	if err := json.Unmarshal(baselineLegacyConfig, &legacy); err != nil {
		t.Fatalf("legacy baseline is not valid JSON: %v", err)
	}
	if _, ok := legacy["rules"]; !ok {
		t.Error("legacy baseline has no rules block")
	}
}

// TestInstallJSTools_RetriesAfterFailureButCachesSuccess is Important 3's
// regression test. installJSTools used to be a sync.Once: correct for a
// single CLI invocation, but wrong for cmd/codebase-analyser-mcp, a
// long-running server - one transient npm failure on the first `analyze`
// call would permanently disable ESLint/tsc for the rest of the process's
// life, since sync.Once caches even a failed run forever. installJSTools
// must retry a failed install on the next call while still caching a
// successful one permanently (and never re-running after that).
//
// jsInstallStep is substituted with a stub so this needs no network access
// and stays offline-safe under `go test -short`.
func TestInstallJSTools_RetriesAfterFailureButCachesSuccess(t *testing.T) {
	origStep := jsInstallStep
	jsInstallMu.Lock()
	origDone := jsInstallDone
	jsInstallDone = false
	jsInstallMu.Unlock()
	t.Cleanup(func() {
		jsInstallStep = origStep
		jsInstallMu.Lock()
		jsInstallDone = origDone
		jsInstallMu.Unlock()
	})

	calls := 0
	jsInstallStep = func() error {
		calls++
		if calls == 1 {
			return errors.New("boom: simulated npm install failure")
		}
		return nil
	}

	if err := installJSTools(); err == nil {
		t.Fatal("installJSTools: got nil error on the first (failing) install, want the simulated failure")
	}
	if err := installJSTools(); err != nil {
		t.Fatalf("installJSTools: retry after a failure = %v, want nil (retry must succeed)", err)
	}
	if err := installJSTools(); err != nil {
		t.Fatalf("installJSTools: third call = %v, want nil (a cached success)", err)
	}
	if calls != 2 {
		t.Fatalf("jsInstallStep called %d times, want 2: one failing call plus one succeeding retry - a cached success must never call it again", calls)
	}
}

func TestJSExcludedDirs(t *testing.T) {
	want := map[string]bool{"node_modules": true, "dist": true, "build": true, ".next": true, "out": true, "coverage": true}
	if len(jsExcludedDirs) != len(want) {
		t.Fatalf("jsExcludedDirs = %v, want %d entries", jsExcludedDirs, len(want))
	}
	for _, d := range jsExcludedDirs {
		if !want[d] {
			t.Errorf("unexpected excluded dir %q", d)
		}
	}
}
