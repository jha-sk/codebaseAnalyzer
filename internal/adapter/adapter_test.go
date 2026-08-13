package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// resetGoBinDirCache clears the memoized goBinDir() result so a test can
// exercise computeGoBinDir()/GOBIN resolution under a controlled
// environment rather than whatever the process first happened to compute.
func resetGoBinDirCache() {
	goBinDirOnce = sync.Once{}
	goBinDirValue = ""
}

// TestCommandExists_findsGoInstalledBinaryViaGOBIN is Critical 1's
// regression test: it deliberately does NOT rely on the developer's real
// PATH containing a Go bin directory. PATH is pointed at an empty temp dir
// (no tool anywhere on it), and the "installed" tool only exists via GOBIN -
// exactly the fresh-machine shape (`go install .../gosec@latest` succeeds,
// nothing put its bin dir on PATH).
func TestCommandExists_findsGoInstalledBinaryViaGOBIN(t *testing.T) {
	resetGoBinDirCache()
	t.Cleanup(resetGoBinDirCache)

	gobin := t.TempDir()
	fake := filepath.Join(gobin, "faketool")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBIN", gobin)
	t.Setenv("PATH", t.TempDir()) // deliberately excludes gobin and any real tool location

	if !commandExists("faketool") {
		t.Fatal("commandExists(\"faketool\") = false, want true via GOBIN even though PATH doesn't include it")
	}

	out, err := runCommand(t.TempDir(), "faketool")
	if err != nil {
		t.Fatalf("runCommand returned error, want success via GOBIN fallback: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hi" {
		t.Errorf("out = %q, want %q", out, "hi")
	}
}

// TestCommandExists_noGoAndNoPathMatchDegradesCleanly covers the "go itself
// isn't installed" case: goBinDir() must return "" rather than panicking or
// hanging, and commandExists must fall back to plain false.
func TestCommandExists_noGoAndNoPathMatchDegradesCleanly(t *testing.T) {
	resetGoBinDirCache()
	t.Cleanup(resetGoBinDirCache)

	t.Setenv("GOBIN", "")
	t.Setenv("PATH", t.TempDir()) // no `go`, no target tool anywhere

	if commandExists("definitely-not-a-real-tool") {
		t.Fatal("commandExists = true, want false when neither PATH nor GOBIN has the tool and `go` is unreachable")
	}
}

// TestRunCommand_nonZeroExitWithStdout mirrors a linter that exits non-zero
// because it found issues: stdout is real output, so it must NOT be treated
// as a failure.
func TestRunCommand_nonZeroExitWithStdout(t *testing.T) {
	out, err := runCommand(t.TempDir(), "sh", "-c", "echo hello; exit 1")
	if err != nil {
		t.Fatalf("runCommand returned error, want success: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("out = %q, want %q", out, "hello")
	}
}

// TestRunCommand_nonZeroExitNoStdout mirrors a genuine tool failure (bad
// flag, panic, missing config): no stdout to parse, so it must surface a
// real error carrying the exit code and captured stderr rather than being
// swallowed.
func TestRunCommand_nonZeroExitNoStdout(t *testing.T) {
	_, err := runCommand(t.TempDir(), "sh", "-c", "echo boom 1>&2; exit 3")
	if err == nil {
		t.Fatal("runCommand returned nil error, want an error")
	}
	if !strings.Contains(err.Error(), "3") {
		t.Errorf("error = %q, want it to mention exit code 3", err.Error())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want it to mention stderr text %q", err.Error(), "boom")
	}
}

// TestComputeGoBinDir_honorsProcessEnvGOBIN verifies that GOBIN set in
// the process environment (including via `go env -w`) is honored by
// consulting the Go toolchain's own view.
func TestComputeGoBinDir_honorsProcessEnvGOBIN(t *testing.T) {
	gobin := t.TempDir()
	t.Setenv("GOBIN", gobin)

	result := computeGoBinDir()
	if result != gobin {
		t.Errorf("computeGoBinDir() = %q, want %q (process env GOBIN)", result, gobin)
	}
}

// TestComputeGoBinDir_fallsBackToGOPATH verifies that when GOBIN is empty,
// the resolver falls back to GOPATH/bin.
func TestComputeGoBinDir_fallsBackToGOPATH(t *testing.T) {
	gopath := t.TempDir()
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", gopath)

	result := computeGoBinDir()
	expected := filepath.Join(gopath, "bin")
	if result != expected {
		t.Errorf("computeGoBinDir() = %q, want %q (GOPATH/bin fallback)", result, expected)
	}
}

// TestOnlyPackageScopedAdaptersAreTargeted pins which adapters implement
// Targeted: the three linters that reason about one package/crate at a time
// can be restricted (and so cached); govulncheck/cargo-audit reason about a
// whole dependency set and have nothing smaller to restrict to.
func TestOnlyPackageScopedAdaptersAreTargeted(t *testing.T) {
	targeted := []ToolAdapter{GolangciLint{}, Gosec{}, Clippy{}}
	for _, a := range targeted {
		if _, ok := a.(Targeted); !ok {
			t.Errorf("%s does not implement Targeted", a.Name())
		}
	}
	// Whole-dependency-set scanners have nothing smaller to be restricted to.
	for _, a := range []ToolAdapter{Govulncheck{}, CargoAudit{}} {
		if _, ok := a.(Targeted); ok {
			t.Errorf("%s implements Targeted but scans a whole dependency set", a.Name())
		}
	}
}

// TestEnvForPathReachesTheToolProcess verifies the toolchain hook's
// variables actually reach the spawned tool process, not just cmd.Env in
// memory.
func TestEnvForPathReachesTheToolProcess(t *testing.T) {
	original := EnvForPath
	t.Cleanup(func() { EnvForPath = original })
	EnvForPath = func(string) []string { return []string{"CODEBASE_ANALYSER_PROBE=set"} }

	// Uses `env`, which is present on every platform this test runs on.
	out, err := runCommand(t.TempDir(), "env")
	if err != nil {
		t.Skipf("env not available: %v", err)
	}
	if !strings.Contains(string(out), "CODEBASE_ANALYSER_PROBE=set") {
		t.Error("EnvForPath variables did not reach the tool process")
	}
}

// TestComputeGoBinDir_degradesWhenGoUnavailable verifies that when `go`
// is not available, computeGoBinDir returns "" rather than panicking.
func TestComputeGoBinDir_degradesWhenGoUnavailable(t *testing.T) {
	// Mock unavailability by pointing PATH at a directory with no `go` binary.
	// This simulates a machine where `go` is not installed.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")

	// Should return "" rather than panic when go command is unavailable.
	result := computeGoBinDir()
	if result != "" {
		t.Errorf("computeGoBinDir() = %q, want empty string when go is unavailable", result)
	}
}
