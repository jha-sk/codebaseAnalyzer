package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"codebase-analyser/internal/finding"
)

type Clippy struct{}

func (Clippy) Name() string { return "clippy" }

// CheckInstalled probes for cargo-clippy specifically, not just cargo:
// `cargo clippy` is a cargo subcommand backed by the separate cargo-clippy
// binary (installed via `rustup component add clippy`), so cargo being
// present says nothing about whether the clippy component is.
func (Clippy) CheckInstalled() bool { return commandExists("cargo-clippy") }

func (Clippy) Install() error {
	return exec.Command("rustup", "component", "add", "clippy").Run()
}

type cargoMessage struct {
	Reason  string `json:"reason"`
	Message *struct {
		Level string `json:"level"`
		Code  *struct {
			Code string `json:"code"`
		} `json:"code"`
		Message string `json:"message"`
		Spans   []struct {
			FileName  string `json:"file_name"`
			LineStart int    `json:"line_start"`
			IsPrimary bool   `json:"is_primary"`
		} `json:"spans"`
	} `json:"message"`
}

var clippyLevelSeverity = map[string]finding.Severity{
	"error":   finding.SeverityHigh,
	"warning": finding.SeverityMedium,
}

// concurrencyLints maps exact clippy lint names to concurrency findings.
// ponytail: curated set; clippy adds new lints regularly and this set will miss them.
// Upgrade path: add names to this table when new concurrency lints are discovered.
var concurrencyLints = map[string]bool{
	"clippy::await_holding_lock":            true,
	"clippy::await_holding_refcell_ref":     true,
	"clippy::await_holding_invalid_type":    true,
	"clippy::mutex_atomic":                  true,
	"clippy::mutex_integer":                 true,
	"clippy::rc_mutex":                      true,
	"clippy::let_underscore_lock":           true,
	"clippy::readonly_write_lock":           true,
	"clippy::significant_drop_tightening":   true,
	"clippy::significant_drop_in_scrutinee": true,
	"clippy::arc_with_non_send_sync":        true,
	"clippy::non_send_fields_in_send_ty":    true,
}

func (c Clippy) Run(path string) ([]finding.Finding, error) {
	return c.RunTargets(path, nil)
}

// RunTargets ignores targets: a crate is the smallest thing clippy can
// type-check, and cache.Units already treats a whole crate as one unit. The
// method exists so Clippy satisfies Targeted and its result gets cached.
func (Clippy) RunTargets(path string, _ []string) ([]finding.Finding, error) {
	out, err := runCommand(path, "cargo", "clippy", "--message-format=json")
	if err != nil {
		return nil, fmt.Errorf("clippy: %w", err)
	}
	findings, err := clippyFindings(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("clippy: %w", err)
	}
	return findings, nil
}

func clippyFindings(r io.Reader) ([]finding.Finding, error) {
	var findings []finding.Finding
	dec := json.NewDecoder(r)
	for {
		var msg cargoMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("parsing output: %w", err)
		}
		if msg.Reason != "compiler-message" || msg.Message == nil || msg.Message.Code == nil {
			continue
		}
		code := msg.Message.Code.Code
		if !strings.HasPrefix(code, "clippy::") {
			continue
		}
		file, line := "", 0
		for _, span := range msg.Message.Spans {
			if span.IsPrimary {
				file, line = span.FileName, span.LineStart
				break
			}
		}
		category := finding.CategoryCorrectness
		if concurrencyLints[code] {
			category = finding.CategoryConcurrency
		}
		severity := clippyLevelSeverity[msg.Message.Level]
		if severity == "" {
			severity = finding.SeverityMedium
		}
		findings = append(findings, finding.Finding{
			File:     file,
			Line:     line,
			Tool:     "clippy",
			RuleID:   code,
			Category: category,
			Severity: severity,
			Message:  msg.Message.Message,
		})
	}
	return findings, nil
}
