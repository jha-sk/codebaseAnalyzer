// internal/adapter/pytools_test.go
package adapter

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestPyToolsDir_honorsCacheOverride(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", "/tmp/fake-cache-root")
	want := filepath.Join("/tmp/fake-cache-root", "py-tools")
	if got := pyToolsDir(); got != want {
		t.Errorf("pyToolsDir() = %q, want %q", got, want)
	}
}

func TestPinnedPyBin_pathShape(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", "/tmp/fake-cache-root")
	want := filepath.Join("/tmp/fake-cache-root", "py-tools", "bin", "ruff")
	if got := pinnedPyBin("ruff"); got != want {
		t.Errorf("pinnedPyBin(\"ruff\") = %q, want %q", got, want)
	}
}

// TestInstallPyTools_retriesAfterFailure mirrors the JS install tests: a
// transient failure (network blip, missing python3) must not permanently
// poison installs for the rest of a long-running process (sync.Once would
// do that wrong) - the next caller must retry.
func TestInstallPyTools_retriesAfterFailure(t *testing.T) {
	original := pyInstallStep
	t.Cleanup(func() { pyInstallStep = original; pyInstallDone = false })

	calls := 0
	pyInstallStep = func() error {
		calls++
		if calls == 1 {
			return fmt.Errorf("simulated failure")
		}
		return nil
	}
	pyInstallDone = false

	if err := installPyTools(); err == nil {
		t.Fatal("first installPyTools() = nil error, want the simulated failure")
	}
	if err := installPyTools(); err != nil {
		t.Fatalf("second installPyTools() = %v, want success (retry after failure)", err)
	}
	if calls != 2 {
		t.Errorf("pyInstallStep called %d times, want 2", calls)
	}
}

// TestInstallPyTools_succeedsOnceThenSkipsRepeatCalls confirms a success is
// cached permanently - the underlying step must not run again.
func TestInstallPyTools_succeedsOnceThenSkipsRepeatCalls(t *testing.T) {
	original := pyInstallStep
	t.Cleanup(func() { pyInstallStep = original; pyInstallDone = false })

	calls := 0
	pyInstallStep = func() error { calls++; return nil }
	pyInstallDone = false

	if err := installPyTools(); err != nil {
		t.Fatal(err)
	}
	if err := installPyTools(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("pyInstallStep called %d times, want 1 (second call must be a no-op)", calls)
	}
}
