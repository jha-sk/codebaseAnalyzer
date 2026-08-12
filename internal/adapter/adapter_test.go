package adapter

import (
	"strings"
	"testing"
)

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
