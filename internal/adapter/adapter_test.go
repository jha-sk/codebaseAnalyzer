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
