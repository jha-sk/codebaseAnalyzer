package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"

	"codebase-analyser/internal/finding"
)

type Govulncheck struct{}

func (Govulncheck) Name() string         { return "govulncheck" }
func (Govulncheck) CheckInstalled() bool { return commandExists("govulncheck") }

func (Govulncheck) Install() error {
	return exec.Command("go", "install", "golang.org/x/vuln/cmd/govulncheck@latest").Run()
}

type govulnMessage struct {
	OSV     *govulnOSV     `json:"osv,omitempty"`
	Finding *govulnFinding `json:"finding,omitempty"`
}

type govulnOSV struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

type govulnFinding struct {
	OSV   string           `json:"osv"`
	Trace []govulnTracePos `json:"trace"`
}

type govulnTracePos struct {
	Function string `json:"function"`
	Position *struct {
		Filename string `json:"filename"`
		Line     int    `json:"line"`
	} `json:"position"`
}

func (Govulncheck) Run(path string) ([]finding.Finding, error) {
	out, err := runCommand(path, "govulncheck", "-json", "./...")
	if err != nil {
		return nil, fmt.Errorf("govulncheck: %w", err)
	}
	findings, err := govulncheckFindings(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("govulncheck: %w", err)
	}
	return findings, nil
}

func govulncheckFindings(r io.Reader) ([]finding.Finding, error) {
	osvSummaries := map[string]string{}
	var rawFindings []govulnFinding

	dec := json.NewDecoder(r)
	for {
		var msg govulnMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("parsing output: %w", err)
		}
		if msg.OSV != nil {
			osvSummaries[msg.OSV.ID] = msg.OSV.Summary
		}
		if msg.Finding != nil {
			rawFindings = append(rawFindings, *msg.Finding)
		}
	}

	findings := make([]finding.Finding, 0, len(rawFindings))
	for _, f := range rawFindings {
		// ponytail: reports the last trace entry with a resolved source position;
		// govulncheck's full call-path isn't otherwise surfaced in v1.
		file, line, reachable := "", 0, false
		for _, t := range f.Trace {
			if t.Position != nil && t.Position.Filename != "" {
				file, line = t.Position.Filename, t.Position.Line
			}
			if t.Function != "" {
				reachable = true
			}
		}
		severity := finding.SeverityHigh
		if reachable {
			severity = finding.SeverityCritical
		}
		findings = append(findings, finding.Finding{
			File:     file,
			Line:     line,
			Tool:     "govulncheck",
			RuleID:   f.OSV,
			Category: finding.CategorySecurity,
			Severity: severity,
			Message:  osvSummaries[f.OSV],
		})
	}
	return findings, nil
}
