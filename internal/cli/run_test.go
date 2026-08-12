package cli

import (
	"bytes"
	"context"
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
	if got := computeExitCode(explained, finding.SeverityHigh); got != 1 {
		t.Fatalf("computeExitCode = %d, want 1 for a finding at threshold", got)
	}
}

func TestComputeExitCode_belowThresholdIsZero(t *testing.T) {
	explained := []finding.ExplainedFinding{{Finding: finding.Finding{Severity: finding.SeverityLow}}}
	if got := computeExitCode(explained, finding.SeverityHigh); got != 0 {
		t.Fatalf("computeExitCode = %d, want 0 for a finding below threshold", got)
	}
}

func TestComputeExitCode_noFindingsIsZero(t *testing.T) {
	if got := computeExitCode(nil, finding.SeverityHigh); got != 0 {
		t.Fatalf("computeExitCode = %d, want 0 for no findings", got)
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
