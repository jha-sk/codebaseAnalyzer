package adapter

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"codebase-analyser/internal/finding"
)

// TestClippy_checkInstalledRequiresCargoClippyNotJustCargo is Important 2's
// regression test: a PATH with `cargo` present but no `cargo-clippy` must
// report as NOT installed, so Install() (rustup component add clippy) gets
// a chance to run instead of CheckInstalled lying and Run failing later
// with an opaque subcommand error.
func TestClippy_checkInstalledRequiresCargoClippyNotJustCargo(t *testing.T) {
	resetGoBinDirCache()
	t.Cleanup(resetGoBinDirCache)

	dir := t.TempDir()
	fakeCargo := filepath.Join(dir, "cargo")
	if err := os.WriteFile(fakeCargo, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("GOBIN", "")

	if (Clippy{}).CheckInstalled() {
		t.Error("CheckInstalled() = true with cargo present but cargo-clippy absent, want false")
	}
}

func TestClippy_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/clippy_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := clippyFindings(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 6 {
		t.Fatalf("got %d findings, want 6", len(findings))
	}
	if findings[0].RuleID != "clippy::bool_comparison" || findings[0].Category != finding.CategoryCorrectness {
		t.Errorf("finding[0] = %+v", findings[0])
	}
	if findings[0].Severity != finding.SeverityMedium {
		t.Errorf("finding[0].Severity = %v, want medium", findings[0].Severity)
	}
	if findings[1].RuleID != "clippy::mutex_atomic" || findings[1].Category != finding.CategoryConcurrency {
		t.Errorf("finding[1] = %+v, want concurrency category", findings[1])
	}
	if findings[1].Severity != finding.SeverityHigh {
		t.Errorf("finding[1].Severity = %v, want high", findings[1].Severity)
	}
	if findings[2].RuleID != "clippy::needless_return" || findings[2].Severity != finding.SeverityMedium {
		t.Errorf("finding[2] = %+v, want clippy::needless_return/medium (unrecognized level %q falls back)", findings[2], "note")
	}
	// Regression test: significant_drop_tightening must be concurrency (was false negative with substring matching)
	if findings[3].RuleID != "clippy::significant_drop_tightening" || findings[3].Category != finding.CategoryConcurrency {
		t.Errorf("finding[3] = %+v, want significant_drop_tightening/concurrency", findings[3])
	}
	// Regression test: search_is_some must be correctness (was false positive: "search" contains "arc")
	if findings[4].RuleID != "clippy::search_is_some" || findings[4].Category != finding.CategoryCorrectness {
		t.Errorf("finding[4] = %+v, want search_is_some/correctness", findings[4])
	}
	// Regression test: unknown lint falls back to correctness
	if findings[5].RuleID != "clippy::unknown_lint_xyz" || findings[5].Category != finding.CategoryCorrectness {
		t.Errorf("finding[5] = %+v, want unknown_lint_xyz/correctness", findings[5])
	}
}
