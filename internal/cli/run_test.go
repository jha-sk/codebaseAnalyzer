package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestExecute_noProjectFound(t *testing.T) {
	var buf bytes.Buffer
	_, err := Execute(context.Background(), &buf, RunConfig{
		Path: t.TempDir(), Format: "json", Severity: finding.SeverityHigh, NoLLM: true,
	})
	if err == nil {
		t.Fatal("expected error when no go.mod/Cargo.toml found")
	}
}

// When no projects are found AND some directories are unreadable, the skipped-
// path notes must reach the user, not be silently dropped. This surfaces the
// fact that the result may be incomplete due to permission issues, not just
// absence of projects.
func TestExecute_noProjectFoundWithUnreadablePath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("cannot create unreadable directories as root")
	}

	tmpdir := t.TempDir()
	unreadable := filepath.Join(tmpdir, "unreadable")
	if err := os.Mkdir(unreadable, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(unreadable, 0o755) // restore permissions for cleanup
	})
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}

	var buf bytes.Buffer
	_, err := Execute(context.Background(), &buf, RunConfig{
		Path: tmpdir, Format: "human", Severity: finding.SeverityHigh, NoLLM: true,
	})
	if err == nil {
		t.Fatal("expected error when no projects found")
	}

	// Error message must acknowledge that directories could not be read
	if !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("expected error to mention unreadable directories, got: %v", err)
	}

	// Skipped-path note must appear in the output (human format writes to w)
	output := buf.String()
	if !strings.Contains(output, "skipped unreadable path") {
		t.Fatalf("expected skipped-path note in output, got: %q", output)
	}
}

// Important 5: an invalid --llm-provider must be caught before Detect runs,
// not after. t.TempDir() has neither go.mod nor Cargo.toml, so if provider
// validation happened after (or was skipped before) Detect, this would fail
// with "no Go or Rust project found" instead of the provider error -
// proving the ordering, not just that some error comes back.
func TestExecute_invalidProviderFailsBeforeDetect(t *testing.T) {
	var buf bytes.Buffer
	_, err := Execute(context.Background(), &buf, RunConfig{
		Path: t.TempDir(), Format: "json", Severity: finding.SeverityHigh, LLMProvider: "bogus-provider",
	})
	if err == nil {
		t.Fatal("expected an error for the invalid --llm-provider")
	}
	if strings.Contains(err.Error(), "no Go or Rust project") {
		t.Fatalf("provider validation must happen before Detect, got: %v", err)
	}
}

// --no-llm must still skip provider validation entirely at the Execute
// level too (not just inside resolveExplanations): a bogus --llm-provider
// paired with --no-llm must reach Detect's "no project found" error, not a
// provider error.
func TestExecute_noLLMSkipsProviderValidationEvenWithBogusProvider(t *testing.T) {
	var buf bytes.Buffer
	_, err := Execute(context.Background(), &buf, RunConfig{
		Path: t.TempDir(), Format: "json", Severity: finding.SeverityHigh, LLMProvider: "bogus-provider", NoLLM: true,
	})
	if err == nil {
		t.Fatal("expected an error (no project found)")
	}
	if !strings.Contains(err.Error(), "no Go or Rust project") {
		t.Fatalf("expected the no-project error since --no-llm should skip provider validation, got: %v", err)
	}
}

func TestFilterCategories(t *testing.T) {
	findings := []finding.Finding{
		{Category: finding.CategorySecurity},
		{Category: finding.CategoryCorrectness},
	}
	out := filterCategories(findings, []finding.Category{finding.CategorySecurity})
	if len(out) != 1 || out[0].Category != finding.CategorySecurity {
		t.Fatalf("got %+v", out)
	}
}

// --no-llm must skip provider selection entirely: even a bogus --llm-provider
// value must not be validated (and must not error) when NoLLM is set.
func TestResolveExplanations_noLLMSkipsProviderSelectionEntirely(t *testing.T) {
	findings := []finding.Finding{{Category: finding.CategorySecurity}}
	out, note, err := resolveExplanations(context.Background(), RunConfig{LLMProvider: "bogus-provider", NoLLM: true}, findings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if note != "" {
		t.Fatalf("expected no note, got %q", note)
	}
	if len(out) != 1 || out[0].Explanation != "" {
		t.Fatalf("got %+v", out)
	}
}

// no provider configured at all (no flag, no env var) is a normal state:
// nil error, raw findings returned, and a note describing how to enable
// explanations for the caller to place per --format.
func TestResolveExplanations_noProviderConfiguredIsNotAnError(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	findings := []finding.Finding{{Category: finding.CategorySecurity}}
	out, note, err := resolveExplanations(context.Background(), RunConfig{}, findings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if note == "" {
		t.Fatal("expected a non-empty note when no provider is configured")
	}
	if len(out) != 1 || out[0].Explanation != "" {
		t.Fatalf("got %+v", out)
	}
}

// An unrecognized --llm-provider value must surface as a real error (the
// user explicitly asked for a provider that can't be used).
func TestResolveExplanations_unknownProviderIsAnError(t *testing.T) {
	_, _, err := resolveExplanations(context.Background(), RunConfig{LLMProvider: "bogus"}, nil)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

// A named provider with no API key configured must also surface as a real
// error, not silently fall back to raw findings.
func TestResolveExplanations_namedProviderMissingKeyIsAnError(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, _, err := resolveExplanations(context.Background(), RunConfig{LLMProvider: "anthropic"}, nil)
	if err == nil {
		t.Fatal("expected error for named provider with no API key")
	}
}

// writeNotes is the reachable seam for the human-vs-json note routing:
// Execute itself can't be driven with fake adapters (orchestrator.Run always
// uses real tools/projects), so this exercises the exact function that
// decides where notes land.
func TestWriteNotes_humanFormatWritesIntoW(t *testing.T) {
	var buf bytes.Buffer
	writeNotes(&buf, "human", []string{"note: skipped gosec: install failed: exit status 1"})
	if !strings.Contains(buf.String(), "skipped gosec") || !strings.Contains(buf.String(), "install failed") {
		t.Fatalf("want tool name and reason in w, got %q", buf.String())
	}
}

func TestWriteNotes_jsonFormatLeavesWUntouched(t *testing.T) {
	var buf bytes.Buffer
	writeNotes(&buf, "json", []string{"note: skipped gosec: install failed: exit status 1"})
	if buf.Len() != 0 {
		t.Fatalf("want w untouched for --format json, got %q", buf.String())
	}
}

func TestWriteNotes_noNotesWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	writeNotes(&buf, "human", nil)
	if buf.Len() != 0 {
		t.Fatalf("want w untouched when there are no notes, got %q", buf.String())
	}
}

// Boundary condition: a finding exactly AT the severity threshold must
// trigger a non-zero exit code, not just findings strictly above it.
func TestComputeExitCode_atThresholdTriggersNonZero(t *testing.T) {
	explained := []finding.ExplainedFinding{{Finding: finding.Finding{Severity: finding.SeverityHigh}}}
	if got := computeExitCode(explained, finding.SeverityHigh, false); got != 1 {
		t.Fatalf("computeExitCode = %d, want 1 for a finding at threshold", got)
	}
}

func TestComputeExitCode_belowThresholdIsZero(t *testing.T) {
	explained := []finding.ExplainedFinding{{Finding: finding.Finding{Severity: finding.SeverityLow}}}
	if got := computeExitCode(explained, finding.SeverityHigh, false); got != 0 {
		t.Fatalf("computeExitCode = %d, want 0 for a finding below threshold", got)
	}
}

func TestComputeExitCode_noFindingsIsZero(t *testing.T) {
	if got := computeExitCode(nil, finding.SeverityHigh, false); got != 0 {
		t.Fatalf("computeExitCode = %d, want 0 for no findings", got)
	}
}

// Critical 2: a run where every tool was skipped must never look like a
// clean pass just because zero tools produced zero findings.
func TestComputeExitCode_allSkippedNoFindingsIsIncomplete(t *testing.T) {
	if got := computeExitCode(nil, finding.SeverityHigh, true); got != 2 {
		t.Fatalf("computeExitCode = %d, want 2 (incomplete coverage) when every tool was skipped and nothing was found", got)
	}
}

// Some tools ran and found nothing above threshold, but others were
// skipped: coverage is still partial, so this must not read as a clean
// pass either.
func TestComputeExitCode_someSkippedBelowThresholdIsIncomplete(t *testing.T) {
	explained := []finding.ExplainedFinding{{Finding: finding.Finding{Severity: finding.SeverityLow}}}
	if got := computeExitCode(explained, finding.SeverityHigh, true); got != 2 {
		t.Fatalf("computeExitCode = %d, want 2 (incomplete coverage) with a below-threshold finding and a skipped tool", got)
	}
}

// A real finding at/above threshold takes priority over the incomplete-
// coverage signal: something was actually found, which is the more urgent
// fact, even though coverage was also partial.
func TestComputeExitCode_thresholdMetWinsOverIncompleteCoverage(t *testing.T) {
	explained := []finding.ExplainedFinding{{Finding: finding.Finding{Severity: finding.SeverityCritical}}}
	if got := computeExitCode(explained, finding.SeverityHigh, true); got != 1 {
		t.Fatalf("computeExitCode = %d, want 1 (threshold met) even though coverage was also incomplete", got)
	}
}

// The fully clean path: no findings at/above threshold, and no tool was
// skipped.
func TestComputeExitCode_noneSkippedCleanPassIsZero(t *testing.T) {
	explained := []finding.ExplainedFinding{{Finding: finding.Finding{Severity: finding.SeverityLow}}}
	if got := computeExitCode(explained, finding.SeverityHigh, false); got != 0 {
		t.Fatalf("computeExitCode = %d, want 0 for a clean run with full coverage", got)
	}
}

func TestNewRunCmd_defaults(t *testing.T) {
	cmd := NewRunCmd()
	f := cmd.Flags()
	if v, _ := f.GetString("format"); v != "human" {
		t.Errorf("format default = %q, want %q", v, "human")
	}
	if v, _ := f.GetString("severity"); v != "high" {
		t.Errorf("severity default = %q, want %q", v, "high")
	}
	if cmd.Use != "run <path>" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestNewRunCmd_invalidSeverityIsAClearError(t *testing.T) {
	cmd := NewRunCmd()
	cmd.SetArgs([]string{t.TempDir(), "--severity", "bogus"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for invalid --severity")
	}
}

func TestNewRunCmd_invalidFormatIsAClearError(t *testing.T) {
	cmd := NewRunCmd()
	cmd.SetArgs([]string{t.TempDir(), "--format", "Json"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid --format")
	}
	if !strings.Contains(err.Error(), "invalid format") {
		t.Errorf("error = %q, want it to mention invalid format", err)
	}
}

func TestNewRunCmd_invalidCategoryIsAClearError(t *testing.T) {
	cmd := NewRunCmd()
	cmd.SetArgs([]string{t.TempDir(), "--category", "bogus"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for invalid --category")
	}
}

func TestNewRunCmd_noProjectFoundReturnsPlainErrorNotExitError(t *testing.T) {
	cmd := NewRunCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{t.TempDir(), "--no-llm"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no project found")
	}
	if _, ok := err.(*ExitError); ok {
		t.Fatalf("expected a plain error, not *ExitError: %v", err)
	}
}
