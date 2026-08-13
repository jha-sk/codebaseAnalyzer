// Package cli wires the detect -> orchestrator -> explain -> report pipeline
// together into a single `run` subcommand and translates its outcome into a
// process exit code.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"codebase-analyser/internal/detect"
	"codebase-analyser/internal/explain"
	"codebase-analyser/internal/finding"
	"codebase-analyser/internal/orchestrator"
	"codebase-analyser/internal/report"
)

type RunConfig struct {
	Path        string
	Format      string
	Severity    finding.Severity
	Categories  []finding.Category
	LLMProvider string
	NoLLM       bool
}

// ExitError carries a specific process exit code back through cobra's
// error-return path. It is used only for the "findings met/exceeded the
// severity threshold" outcome, which is a normal result (the report was
// already printed) rather than a failure worth an "Error: ..." line -
// RunE marks the command SilenceErrors when it returns one so cobra stays
// quiet and main() is the sole place that turns it into os.Exit.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }
func (e *ExitError) ExitCode() int { return e.Code }

func NewRunCmd() *cobra.Command {
	cfg := RunConfig{}
	var severityFlag string
	var categoryFlags []string

	cmd := &cobra.Command{
		Use:   "run <path>",
		Short: "Analyse a Go/Rust codebase for production-safety issues",
		Long: `Analyse a Go/Rust codebase for production-safety issues.

Exit codes:
  0  clean pass: no finding at/above --severity, full tool coverage
  1  at least one finding at/above --severity
  2  no finding at/above --severity, but coverage was incomplete
     (a tool was skipped - install failure, crash, timeout, or missing
     binary; see the report/notes for which one and why)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past this point failures are runtime outcomes, not misuse of
			// the command line, so a usage dump would only add noise.
			cmd.SilenceUsage = true

			cfg.Path = args[0]
			if cfg.Format != "human" && cfg.Format != "json" {
				return fmt.Errorf("invalid format %q (want human|json)", cfg.Format)
			}
			sev, err := finding.ParseSeverity(severityFlag)
			if err != nil {
				return err
			}
			cfg.Severity = sev
			for _, c := range categoryFlags {
				cat, err := finding.ParseCategory(c)
				if err != nil {
					return err
				}
				cfg.Categories = append(cfg.Categories, cat)
			}

			exitCode, err := Execute(cmd.Context(), cmd.OutOrStdout(), cfg)
			if err != nil {
				return err
			}
			if exitCode != 0 {
				cmd.SilenceErrors = true
				return &ExitError{Code: exitCode}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&cfg.Format, "format", "human", "human | json")
	cmd.Flags().StringVar(&severityFlag, "severity", "high", "minimum severity to fail on: critical|high|medium|low")
	cmd.Flags().StringSliceVar(&categoryFlags, "category", nil, "restrict to categories (default: all)")
	cmd.Flags().StringVar(&cfg.LLMProvider, "llm-provider", "", "override provider auto-detection")
	cmd.Flags().BoolVar(&cfg.NoLLM, "no-llm", false, "skip explanations entirely (raw findings only)")
	return cmd
}

// Execute runs the full pipeline and returns the process exit code.
//
// For --format json, w carries nothing but the JSON document: diagnostic
// notes (skipped tools, no LLM provider configured) go to os.Stderr instead,
// so redirecting stdout alone still yields parseable JSON.
//
// For --format human there is no such stream-purity constraint, and
// report.RenderHuman has no concept of notes to fold them into - so instead
// the notes are written into w themselves, ahead of the report, so a user
// who redirects `analyser run . > report.txt` still has the warning on
// record. They are not duplicated to stderr for this format: w already
// lands in the terminal in the interactive case, and duplicating would just
// be noise there.
func Execute(ctx context.Context, w io.Writer, cfg RunConfig) (int, error) {
	// Validate LLM provider selection before Detect/orchestrator.Run - a bad
	// --llm-provider value or a named provider with no API key used to
	// surface only after the entire (potentially minutes-long) analysis
	// finished, discarding every finding. resolveExplanations below repeats
	// this same check once actual findings exist; that's fine; it's a pure
	// env-var/flag check, not I/O.
	if !cfg.NoLLM {
		if _, _, _, err := explain.SelectProvider(cfg.LLMProvider, os.Getenv); err != nil {
			return 1, err
		}
	}

	projects, skippedPaths, err := detect.Detect(cfg.Path)
	if err != nil {
		return 1, err
	}
	if len(projects) == 0 {
		// Surface skipped-path notes even though no projects were found,
		// so the user knows the result may be incomplete.
		var notes []string
		for _, s := range skippedPaths {
			notes = append(notes, "note: skipped unreadable path during detection: "+s)
		}
		writeNotes(w, cfg.Format, notes)

		// Return error about no projects found, acknowledging any skipped paths
		// so the user knows this may be a permission issue, not actual absence.
		if len(skippedPaths) > 0 {
			return 1, fmt.Errorf("no Go or Rust project found under %s, and %d directories could not be read", cfg.Path, len(skippedPaths))
		}
		return 1, fmt.Errorf("no Go or Rust project found under %s", cfg.Path)
	}

	results := orchestrator.Run(projects, orchestrator.DefaultAdapters)
	var findings []finding.Finding
	var notes []string
	var skippedTools []report.SkippedTool
	for _, r := range results {
		if r.Skipped {
			notes = append(notes, fmt.Sprintf("note: skipped %s (%s): %v", r.Tool, r.Path, r.Error))
			skippedTools = append(skippedTools, report.SkippedTool{Tool: r.Tool, Path: r.Path, Reason: r.Error.Error()})
			continue
		}
		findings = append(findings, r.Findings...)
	}
	for _, s := range skippedPaths {
		notes = append(notes, "note: skipped unreadable path during detection: "+s)
	}

	// Filter categories before spending any LLM calls on findings that are
	// about to be discarded.
	if len(cfg.Categories) > 0 {
		findings = filterCategories(findings, cfg.Categories)
	}

	explained, providerNote, err := resolveExplanations(ctx, cfg, findings)
	if err != nil {
		return 1, err
	}
	if providerNote != "" {
		notes = append(notes, providerNote)
	}

	writeNotes(w, cfg.Format, notes)

	if cfg.Format == "json" {
		if err := report.RenderJSON(w, explained, skippedTools); err != nil {
			return 1, err
		}
	} else {
		report.RenderHuman(w, explained, skippedTools)
	}

	return computeExitCode(explained, cfg.Severity, len(skippedTools) > 0), nil
}

// writeNotes surfaces diagnostic notes (skipped tools, no LLM provider
// configured) where they'll actually be seen for the given format: into w
// itself, ahead of the report, for human output that might be redirected to
// a file; to stderr for json output, so the JSON document on w stays pure.
func writeNotes(w io.Writer, format string, notes []string) {
	if len(notes) == 0 {
		return
	}
	dest := w
	if format == "json" {
		dest = os.Stderr
	}
	for _, n := range notes {
		fmt.Fprintln(dest, n)
	}
	if dest == w {
		fmt.Fprintln(dest) // blank line separating notes from the report that follows
	}
}

// computeExitCode picks the process exit code (see the `run` command's Long
// help text for the contract): 1 if any finding is at/above threshold - a
// tool being skipped never masks that; 2 if nothing hit threshold but
// coverage was incomplete (some tool was skipped) - a run where every
// analyzer was skipped must never look like a clean pass just because zero
// tools produced zero findings; 0 otherwise.
func computeExitCode(explained []finding.ExplainedFinding, threshold finding.Severity, incompleteCoverage bool) int {
	for _, f := range explained {
		if finding.MeetsThreshold(f.Severity, threshold) {
			return 1
		}
	}
	if incompleteCoverage {
		return 2
	}
	return 0
}

// resolveExplanations decides whether to call an LLM at all. --no-llm skips
// provider selection entirely - not even an invalid --llm-provider value is
// validated in that case. A named/detected provider that can't actually be
// used (unrecognized name, or missing API key) is a real error, since the
// user (or an env var they set) explicitly asked for it. No provider
// configured anywhere is a normal state: it returns a note (for the caller
// to place per --format) and continues with raw findings.
func resolveExplanations(ctx context.Context, cfg RunConfig, findings []finding.Finding) ([]finding.ExplainedFinding, string, error) {
	if cfg.NoLLM {
		return finding.WithoutExplanation(findings), "", nil
	}
	explainer, _, ok, err := explain.SelectProvider(cfg.LLMProvider, os.Getenv)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		note := "note: no LLM provider configured (set ANTHROPIC_API_KEY, OPENAI_API_KEY, or GEMINI_API_KEY); showing raw findings only"
		return finding.WithoutExplanation(findings), note, nil
	}
	return explain.Group(ctx, explainer, findings), "", nil
}

func filterCategories(findings []finding.Finding, allowed []finding.Category) []finding.Finding {
	allowedSet := map[finding.Category]bool{}
	for _, c := range allowed {
		allowedSet[c] = true
	}
	var out []finding.Finding
	for _, f := range findings {
		if allowedSet[f.Category] {
			out = append(out, f)
		}
	}
	return out
}
