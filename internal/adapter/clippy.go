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

func (Clippy) Name() string         { return "clippy" }
func (Clippy) CheckInstalled() bool { return commandExists("cargo") }

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

// ponytail: lint names containing these substrings are filed under concurrency;
// everything else defaults to correctness. Extend if a concurrency lint slips through.
var concurrencyLintHints = []string{"mutex", "lock", "arc", "atomic", "send", "sync"}

func (Clippy) Run(path string) ([]finding.Finding, error) {
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
		for _, hint := range concurrencyLintHints {
			if strings.Contains(code, hint) {
				category = finding.CategoryConcurrency
				break
			}
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
