# Codebase Analyser CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `analyser` CLI — detects Go/Rust projects, runs wrapped static-analysis tools concurrently, normalizes findings, batch-explains them via an LLM, and renders a human or JSON report with a severity-gated exit code.

**Architecture:** Linear pipeline (`detect → run adapters → explain → render → exit code`) as five independent packages under `internal/`, wired together by a thin `cli` package and a two-line `main.go`. Each stage is a pure function or a small interface (`ToolAdapter`, `Explainer`) so it's unit-testable without shelling out to real tools or calling a real LLM.

**Tech Stack:** Go 1.22+, `github.com/spf13/cobra` for the CLI, stdlib `os/exec` + `encoding/json` for tool wrapping, stdlib `net/http` for LLM calls (no SDK deps — each provider is a handful of JSON fields over one endpoint).

**Spec:** [docs/superpowers/specs/2026-08-13-codebase-analyser-cli-design.md](../specs/2026-08-13-codebase-analyser-cli-design.md)

## Global Constraints

- Go only, single binary `analyser` (spec: Implementation language).
- Tools wrapped, exactly these five, via CLI + JSON output — never import their internals: `golangci-lint` (govet, errcheck, staticcheck, contextcheck, bodyclose, noctx enabled), `gosec`, `govulncheck` for Go; `clippy`, `cargo-audit` for Rust.
- `Category` ∈ `correctness | concurrency | security | operational`; `Severity` ∈ `critical | high | medium | low`, normalized per-tool in each adapter.
- LLM provider priority: `ANTHROPIC_API_KEY` > `OPENAI_API_KEY` > `GEMINI_API_KEY`, overridable by `--llm-provider`. No key set → raw findings, no explanations, no error.
- Explanations batched one call per `(tool, ruleID)` group, reused across all instances in the run. LLM failure → that group falls back to unexplained findings; never blocks the report.
- Default per-tool timeout: 5 minutes. Missing tool → auto-install once, visible message; install failure → tool skipped, noted in report, run continues.
- No project config file in v1 — flags only: `--format`, `--severity` (default `high`), `--category`, `--llm-provider`, `--no-llm`.
- No project detected (`go.mod`/`Cargo.toml` absent) → clear error, non-zero exit, no empty report.
- Exit code non-zero if any finding is at/above `--severity` threshold.

---

## File Structure

```
codebase-analyser/
├── go.mod
├── cmd/analyser/
│   ├── main.go
│   └── main_test.go
├── internal/
│   ├── finding/
│   │   ├── finding.go          # Category, Severity, Finding, ExplainedFinding
│   │   └── finding_test.go
│   ├── detect/
│   │   ├── detect.go
│   │   └── detect_test.go
│   ├── adapter/
│   │   ├── adapter.go          # ToolAdapter interface, exec helper
│   │   ├── golangci.go / golangci_test.go / testdata/golangci_sample.json
│   │   ├── gosec.go / gosec_test.go / testdata/gosec_sample.json
│   │   ├── govulncheck.go / govulncheck_test.go / testdata/govulncheck_sample.json
│   │   ├── clippy.go / clippy_test.go / testdata/clippy_sample.json
│   │   └── cargoaudit.go / cargoaudit_test.go / testdata/cargoaudit_sample.json
│   ├── orchestrator/
│   │   ├── orchestrator.go
│   │   └── orchestrator_test.go
│   ├── explain/
│   │   ├── explain.go          # Explainer interface, Group() batching
│   │   ├── explain_test.go
│   │   ├── providers.go        # buildPrompt/parseExplanation shared helpers, SelectProvider
│   │   ├── anthropic.go
│   │   ├── openai.go
│   │   ├── gemini.go
│   │   └── providers_test.go
│   ├── report/
│   │   ├── human.go / human_test.go
│   │   └── json.go / json_test.go
│   └── cli/
│       ├── run.go              # cobra command + Execute()
│       └── run_test.go
└── testdata/fixtures/
    ├── go-repo/{go.mod,bad.go}
    └── rust-repo/{Cargo.toml,src/main.rs}
```

---

### Task 1: Module scaffold + CLI entrypoint

**Files:**
- Create: `go.mod`
- Create: `cmd/analyser/main.go`
- Test: `cmd/analyser/main_test.go`

**Interfaces:**
- Produces: `newRootCmd() *cobra.Command` — the bare root command, extended by later tasks.

- [ ] **Step 1: Init module and add cobra**

```bash
cd /home/sourabh/Projects/NewProjects/codebase-analyser
go mod init codebase-analyser
go get github.com/spf13/cobra@latest
```

- [ ] **Step 2: Write the failing test**

```go
// cmd/analyser/main_test.go
package main

import "testing"

func TestNewRootCmd(t *testing.T) {
	cmd := newRootCmd()
	if cmd.Use != "analyser" {
		t.Errorf("Use = %q, want %q", cmd.Use, "analyser")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./cmd/...`
Expected: FAIL — `newRootCmd` undefined

- [ ] **Step 4: Write minimal implementation**

```go
// cmd/analyser/main.go
package main

import (
	"os"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "analyser",
		Short: "Analyse Go/Rust codebases for production-safety issues",
	}
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./cmd/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git init
git add go.mod go.sum cmd/analyser/main.go cmd/analyser/main_test.go
git commit -m "chore: scaffold analyser CLI module"
```

---

### Task 2: `finding` package — Category, Severity, Finding

**Files:**
- Create: `internal/finding/finding.go`
- Test: `internal/finding/finding_test.go`

**Interfaces:**
- Produces: `Category` (`CategoryCorrectness|CategoryConcurrency|CategorySecurity|CategoryOperational`), `Severity` (`SeverityCritical|SeverityHigh|SeverityMedium|SeverityLow`), `ParseCategory(string) (Category, error)`, `ParseSeverity(string) (Severity, error)`, `MeetsThreshold(sev, threshold Severity) bool`, `Finding{File string, Line int, Tool, RuleID string, Category Category, Severity Severity, Message string}`, `ExplainedFinding{Finding; Explanation, FixPattern string}`, `WithoutExplanation([]Finding) []ExplainedFinding`. Every later package imports these exact names.

- [ ] **Step 1: Write the failing tests**

```go
// internal/finding/finding_test.go
package finding

import "testing"

func TestParseSeverity(t *testing.T) {
	if _, err := ParseSeverity("bogus"); err == nil {
		t.Fatal("expected error for invalid severity")
	}
	sev, err := ParseSeverity("high")
	if err != nil || sev != SeverityHigh {
		t.Fatalf("got %v, %v; want SeverityHigh, nil", sev, err)
	}
}

func TestParseCategory(t *testing.T) {
	if _, err := ParseCategory("bogus"); err == nil {
		t.Fatal("expected error for invalid category")
	}
	cat, err := ParseCategory("security")
	if err != nil || cat != CategorySecurity {
		t.Fatalf("got %v, %v; want CategorySecurity, nil", cat, err)
	}
}

func TestMeetsThreshold(t *testing.T) {
	cases := []struct {
		sev, threshold Severity
		want           bool
	}{
		{SeverityCritical, SeverityHigh, true},
		{SeverityHigh, SeverityHigh, true},
		{SeverityMedium, SeverityHigh, false},
		{SeverityLow, SeverityLow, true},
	}
	for _, c := range cases {
		if got := MeetsThreshold(c.sev, c.threshold); got != c.want {
			t.Errorf("MeetsThreshold(%v, %v) = %v, want %v", c.sev, c.threshold, got, c.want)
		}
	}
}

func TestWithoutExplanation(t *testing.T) {
	in := []Finding{{Tool: "gosec", RuleID: "G101"}}
	out := WithoutExplanation(in)
	if len(out) != 1 || out[0].Tool != "gosec" || out[0].Explanation != "" {
		t.Fatalf("got %+v", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/finding/...`
Expected: FAIL — package doesn't exist yet

- [ ] **Step 3: Write minimal implementation**

```go
// internal/finding/finding.go
package finding

import "fmt"

type Category string

const (
	CategoryCorrectness Category = "correctness"
	CategoryConcurrency Category = "concurrency"
	CategorySecurity    Category = "security"
	CategoryOperational Category = "operational"
)

func ParseCategory(s string) (Category, error) {
	switch Category(s) {
	case CategoryCorrectness, CategoryConcurrency, CategorySecurity, CategoryOperational:
		return Category(s), nil
	}
	return "", fmt.Errorf("invalid category %q (want correctness|concurrency|security|operational)", s)
}

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

var severityRank = map[Severity]int{
	SeverityLow:      1,
	SeverityMedium:   2,
	SeverityHigh:     3,
	SeverityCritical: 4,
}

func ParseSeverity(s string) (Severity, error) {
	if _, ok := severityRank[Severity(s)]; ok {
		return Severity(s), nil
	}
	return "", fmt.Errorf("invalid severity %q (want critical|high|medium|low)", s)
}

// MeetsThreshold reports whether sev is at or above threshold.
func MeetsThreshold(sev, threshold Severity) bool {
	return severityRank[sev] >= severityRank[threshold]
}

type Finding struct {
	File     string
	Line     int
	Tool     string
	RuleID   string
	Category Category
	Severity Severity
	Message  string
}

type ExplainedFinding struct {
	Finding
	Explanation string
	FixPattern  string
}

func WithoutExplanation(findings []Finding) []ExplainedFinding {
	out := make([]ExplainedFinding, len(findings))
	for i, f := range findings {
		out[i] = ExplainedFinding{Finding: f}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/finding/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/finding
git commit -m "feat: add finding package with Category, Severity, Finding types"
```

---

### Task 3: `detect` package

**Files:**
- Create: `internal/detect/detect.go`
- Test: `internal/detect/detect_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Project{Path, Language string}` (`Language` is `"go"` or `"rust"`), `Detect(root string) ([]Project, error)`.

- [ ] **Step 1: Write the failing test**

```go
// internal/detect/detect_test.go
package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetect(t *testing.T) {
	root := t.TempDir()
	goDir := filepath.Join(root, "svc")
	rustDir := filepath.Join(root, "sidecar")
	os.MkdirAll(goDir, 0o755)
	os.MkdirAll(rustDir, 0o755)
	os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module svc\n"), 0o644)
	os.WriteFile(filepath.Join(rustDir, "Cargo.toml"), []byte("[package]\n"), 0o644)

	projects, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2: %+v", len(projects), projects)
	}
	byLang := map[string]string{}
	for _, p := range projects {
		byLang[p.Language] = p.Path
	}
	if byLang["go"] != goDir {
		t.Errorf("go project path = %q, want %q", byLang["go"], goDir)
	}
	if byLang["rust"] != rustDir {
		t.Errorf("rust project path = %q, want %q", byLang["rust"], rustDir)
	}
}

func TestDetectNone(t *testing.T) {
	root := t.TempDir()
	projects, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("got %d projects, want 0", len(projects))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/detect/...`
Expected: FAIL — package doesn't exist yet

- [ ] **Step 3: Write minimal implementation**

```go
// internal/detect/detect.go
package detect

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

type Project struct {
	Path     string
	Language string // "go" or "rust"
}

// Detect walks root for go.mod / Cargo.toml, one Project per directory found.
// A repo may contain both languages.
func Detect(root string) ([]Project, error) {
	var projects []Project
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "target":
				return filepath.SkipDir
			}
			return nil
		}
		switch d.Name() {
		case "go.mod":
			projects = append(projects, Project{Path: filepath.Dir(path), Language: "go"})
		case "Cargo.toml":
			projects = append(projects, Project{Path: filepath.Dir(path), Language: "rust"})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("detect: %w", err)
	}
	return projects, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/detect/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/detect
git commit -m "feat: add project detection for go.mod/Cargo.toml"
```

---

### Task 4: `adapter` package — ToolAdapter interface + golangci-lint

**Files:**
- Create: `internal/adapter/adapter.go`
- Create: `internal/adapter/golangci.go`
- Test: `internal/adapter/golangci_test.go`
- Test data: `internal/adapter/testdata/golangci_sample.json`

**Interfaces:**
- Consumes: `finding.Finding`, `finding.Category*`, `finding.Severity*` from [[finding]].
- Produces: `ToolAdapter` interface (`Name() string`, `CheckInstalled() bool`, `Install() error`, `Run(path string) ([]finding.Finding, error)`), `DefaultTimeout`, `GolangciLint{}` implementing it. Every subsequent adapter task implements the same interface.

- [ ] **Step 1: Write adapter.go (shared, no test of its own — exercised via golangci_test.go)**

```go
// internal/adapter/adapter.go
package adapter

import (
	"context"
	"os/exec"
	"time"

	"codebase-analyser/internal/finding"
)

// DefaultTimeout is how long a single tool run may take before being killed.
const DefaultTimeout = 5 * time.Minute

// ToolAdapter wraps one external static-analysis tool.
type ToolAdapter interface {
	Name() string
	CheckInstalled() bool
	Install() error
	Run(path string) ([]finding.Finding, error)
}

// runCommand executes name with args in dir and returns stdout, respecting
// DefaultTimeout. Linters commonly exit non-zero when they find issues —
// that's not a run failure, so a plain ExitError is swallowed and stdout is
// returned as-is; a bad/empty stdout will fail loudly at JSON-parse time instead.
func runCommand(dir, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if _, ok := err.(*exec.ExitError); ok {
		return out, nil
	}
	return out, err
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
```

- [ ] **Step 2: Author the sample fixture**

```json
// internal/adapter/testdata/golangci_sample.json
{
  "Issues": [
    {
      "FromLinter": "errcheck",
      "Text": "Error return value of `os.Open` is not checked",
      "Pos": {"Filename": "bad.go", "Line": 8, "Column": 2}
    },
    {
      "FromLinter": "staticcheck",
      "Text": "SA2001: empty critical section, did you mean to defer the unlock?",
      "Pos": {"Filename": "worker.go", "Line": 21, "Column": 3}
    }
  ]
}
```

- [ ] **Step 3: Write the failing test**

```go
// internal/adapter/golangci_test.go
package adapter

import (
	"encoding/json"
	"os"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestGolangciLint_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/golangci_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed golangciOutput
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	findings := golangciFindings(parsed)

	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(findings))
	}

	f0 := findings[0]
	if f0.File != "bad.go" || f0.Line != 8 || f0.Tool != "golangci-lint" || f0.RuleID != "errcheck" {
		t.Errorf("finding[0] = %+v", f0)
	}
	if f0.Category != finding.CategoryCorrectness || f0.Severity != finding.SeverityHigh {
		t.Errorf("finding[0] category/severity = %v/%v", f0.Category, f0.Severity)
	}

	f1 := findings[1]
	if f1.RuleID != "SA2001" || f1.Category != finding.CategoryConcurrency {
		t.Errorf("finding[1] = %+v, want RuleID=SA2001 category=concurrency", f1)
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/adapter/... -run TestGolangciLint_parse`
Expected: FAIL — `golangciOutput`/`golangciFindings` undefined

- [ ] **Step 5: Write minimal implementation**

```go
// internal/adapter/golangci.go
package adapter

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"codebase-analyser/internal/finding"
)

type GolangciLint struct{}

func (GolangciLint) Name() string       { return "golangci-lint" }
func (GolangciLint) CheckInstalled() bool { return commandExists("golangci-lint") }

func (GolangciLint) Install() error {
	return exec.Command("go", "install", "github.com/golangci/golangci-lint/cmd/golangci-lint@latest").Run()
}

type golangciOutput struct {
	Issues []golangciIssue `json:"Issues"`
}

type golangciIssue struct {
	FromLinter string `json:"FromLinter"`
	Text       string `json:"Text"`
	Pos        struct {
		Filename string `json:"Filename"`
		Line     int    `json:"Line"`
	} `json:"Pos"`
}

// linterCategory maps each explicitly-enabled linter to its default Finding category.
// ponytail: heuristic first pass, not a rule-by-rule table; revisit if a linter's
// findings consistently land in the wrong category.
var linterCategory = map[string]finding.Category{
	"govet":        finding.CategoryCorrectness,
	"errcheck":     finding.CategoryCorrectness,
	"staticcheck":  finding.CategoryCorrectness,
	"contextcheck": finding.CategoryOperational,
	"bodyclose":    finding.CategoryOperational,
	"noctx":        finding.CategoryOperational,
}

var linterSeverity = map[string]finding.Severity{
	"govet":        finding.SeverityHigh,
	"errcheck":     finding.SeverityHigh,
	"staticcheck":  finding.SeverityMedium,
	"contextcheck": finding.SeverityMedium,
	"bodyclose":    finding.SeverityMedium,
	"noctx":        finding.SeverityMedium,
}

// concurrencyRules lists staticcheck SA codes that specifically catch concurrency
// misuse (per the spec's concurrency scope note). Extend as more are identified.
var concurrencyRules = map[string]bool{
	"SA2001": true, // empty critical section
	"SA2002": true, // called testing.T.FailNow from a goroutine
}

func (GolangciLint) Run(path string) ([]finding.Finding, error) {
	out, err := runCommand(path, "golangci-lint", "run", "--out-format", "json",
		"--enable", "govet,errcheck,staticcheck,contextcheck,bodyclose,noctx")
	if err != nil {
		return nil, fmt.Errorf("golangci-lint: %w", err)
	}
	var parsed golangciOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("golangci-lint: parsing output: %w", err)
	}
	return golangciFindings(parsed), nil
}

func golangciFindings(parsed golangciOutput) []finding.Finding {
	findings := make([]finding.Finding, 0, len(parsed.Issues))
	for _, issue := range parsed.Issues {
		ruleID := extractStaticcheckRule(issue.Text)
		category := linterCategory[issue.FromLinter]
		if category == "" {
			category = finding.CategoryCorrectness
		}
		if issue.FromLinter == "staticcheck" && concurrencyRules[ruleID] {
			category = finding.CategoryConcurrency
		}
		severity := linterSeverity[issue.FromLinter]
		if severity == "" {
			severity = finding.SeverityMedium
		}
		findings = append(findings, finding.Finding{
			File:     issue.Pos.Filename,
			Line:     issue.Pos.Line,
			Tool:     "golangci-lint",
			RuleID:   pickRuleID(issue.FromLinter, ruleID),
			Category: category,
			Severity: severity,
			Message:  issue.Text,
		})
	}
	return findings
}

// extractStaticcheckRule pulls the leading "SAxxxx" code off a staticcheck message.
func extractStaticcheckRule(text string) string {
	if strings.HasPrefix(text, "SA") {
		if idx := strings.Index(text, ":"); idx > 0 {
			return text[:idx]
		}
	}
	return ""
}

func pickRuleID(linter, ruleID string) string {
	if ruleID != "" {
		return ruleID
	}
	return linter
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/adapter/... -run TestGolangciLint_parse`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/adapter/adapter.go internal/adapter/golangci.go internal/adapter/golangci_test.go internal/adapter/testdata/golangci_sample.json
git commit -m "feat: add ToolAdapter interface and golangci-lint adapter"
```

---

### Task 5: `gosec` adapter

**Files:**
- Create: `internal/adapter/gosec.go`
- Test: `internal/adapter/gosec_test.go`
- Test data: `internal/adapter/testdata/gosec_sample.json`

**Interfaces:**
- Consumes: `ToolAdapter`, `finding.Finding` from [[adapter]]/[[finding]].
- Produces: `Gosec{}` implementing `ToolAdapter`.

- [ ] **Step 1: Author the sample fixture**

```json
// internal/adapter/testdata/gosec_sample.json
{
  "Issues": [
    {
      "severity": "HIGH",
      "confidence": "HIGH",
      "rule_id": "G101",
      "details": "Potential hardcoded credentials",
      "file": "bad.go",
      "line": "6"
    },
    {
      "severity": "LOW",
      "confidence": "MEDIUM",
      "rule_id": "G104",
      "details": "Errors unhandled",
      "file": "bad.go",
      "line": "8-9"
    }
  ]
}
```

- [ ] **Step 2: Write the failing test**

```go
// internal/adapter/gosec_test.go
package adapter

import (
	"encoding/json"
	"os"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestGosec_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/gosec_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed gosecOutput
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	findings := gosecFindings(parsed)

	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(findings))
	}
	if findings[0].Severity != finding.SeverityCritical || findings[0].Category != finding.CategorySecurity {
		t.Errorf("finding[0] = %+v", findings[0])
	}
	if findings[1].Line != 8 {
		t.Errorf("finding[1].Line = %d, want 8 (parsed from %q)", findings[1].Line, "8-9")
	}
	if findings[1].Severity != finding.SeverityMedium {
		t.Errorf("finding[1].Severity = %v, want medium", findings[1].Severity)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/adapter/... -run TestGosec_parse`
Expected: FAIL — `gosecOutput`/`gosecFindings` undefined

- [ ] **Step 4: Write minimal implementation**

```go
// internal/adapter/gosec.go
package adapter

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"codebase-analyser/internal/finding"
)

type Gosec struct{}

func (Gosec) Name() string         { return "gosec" }
func (Gosec) CheckInstalled() bool { return commandExists("gosec") }

func (Gosec) Install() error {
	return exec.Command("go", "install", "github.com/securego/gosec/v2/cmd/gosec@latest").Run()
}

type gosecOutput struct {
	Issues []gosecIssue `json:"Issues"`
}

type gosecIssue struct {
	Severity string `json:"severity"`
	RuleID   string `json:"rule_id"`
	Details  string `json:"details"`
	File     string `json:"file"`
	Line     string `json:"line"`
}

var gosecSeverity = map[string]finding.Severity{
	"HIGH":   finding.SeverityCritical,
	"MEDIUM": finding.SeverityHigh,
	"LOW":    finding.SeverityMedium,
}

func (Gosec) Run(path string) ([]finding.Finding, error) {
	out, err := runCommand(path, "gosec", "-fmt=json", "./...")
	if err != nil {
		return nil, fmt.Errorf("gosec: %w", err)
	}
	var parsed gosecOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("gosec: parsing output: %w", err)
	}
	return gosecFindings(parsed), nil
}

func gosecFindings(parsed gosecOutput) []finding.Finding {
	findings := make([]finding.Finding, 0, len(parsed.Issues))
	for _, issue := range parsed.Issues {
		line, _ := strconv.Atoi(strings.SplitN(issue.Line, "-", 2)[0])
		severity := gosecSeverity[issue.Severity]
		if severity == "" {
			severity = finding.SeverityMedium
		}
		findings = append(findings, finding.Finding{
			File:     issue.File,
			Line:     line,
			Tool:     "gosec",
			RuleID:   issue.RuleID,
			Category: finding.CategorySecurity,
			Severity: severity,
			Message:  issue.Details,
		})
	}
	return findings
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/adapter/... -run TestGosec_parse`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/gosec.go internal/adapter/gosec_test.go internal/adapter/testdata/gosec_sample.json
git commit -m "feat: add gosec adapter"
```

---

### Task 6: `govulncheck` adapter

**Files:**
- Create: `internal/adapter/govulncheck.go`
- Test: `internal/adapter/govulncheck_test.go`
- Test data: `internal/adapter/testdata/govulncheck_sample.json`

**Interfaces:**
- Consumes: `ToolAdapter`, `finding.Finding` from [[adapter]]/[[finding]].
- Produces: `Govulncheck{}` implementing `ToolAdapter`.

`govulncheck -json` streams newline-delimited JSON objects, each with one of `config`/`progress`/`osv`/`finding` populated. We only need `osv` (for the summary text) and `finding` (for the vulnerable OSV ID + trace).

- [ ] **Step 1: Author the sample fixture (concatenated JSON stream, as govulncheck emits it)**

```json
// internal/adapter/testdata/govulncheck_sample.json
{"osv":{"id":"GO-2023-1234","summary":"Denial of service via crafted input"}}
{"finding":{"osv":"GO-2023-1234","trace":[{"function":"Parse","position":{"filename":"bad.go","line":15}}]}}
```

- [ ] **Step 2: Write the failing test**

```go
// internal/adapter/govulncheck_test.go
package adapter

import (
	"bytes"
	"os"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestGovulncheck_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/govulncheck_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := govulncheckFindings(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	f := findings[0]
	if f.RuleID != "GO-2023-1234" || f.File != "bad.go" || f.Line != 15 {
		t.Errorf("finding = %+v", f)
	}
	if f.Category != finding.CategorySecurity || f.Severity != finding.SeverityCritical {
		t.Errorf("category/severity = %v/%v, want security/critical", f.Category, f.Severity)
	}
	if f.Message != "Denial of service via crafted input" {
		t.Errorf("message = %q", f.Message)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/adapter/... -run TestGovulncheck_parse`
Expected: FAIL — `govulncheckFindings` undefined

- [ ] **Step 4: Write minimal implementation**

```go
// internal/adapter/govulncheck.go
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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/adapter/... -run TestGovulncheck_parse`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/govulncheck.go internal/adapter/govulncheck_test.go internal/adapter/testdata/govulncheck_sample.json
git commit -m "feat: add govulncheck adapter"
```

---

### Task 7: `clippy` adapter

**Files:**
- Create: `internal/adapter/clippy.go`
- Test: `internal/adapter/clippy_test.go`
- Test data: `internal/adapter/testdata/clippy_sample.json`

**Interfaces:**
- Consumes: `ToolAdapter`, `finding.Finding` from [[adapter]]/[[finding]].
- Produces: `Clippy{}` implementing `ToolAdapter`.

`cargo clippy --message-format=json` streams cargo messages; only `reason:"compiler-message"` entries whose `message.code.code` starts with `clippy::` are lint findings.

- [ ] **Step 1: Author the sample fixture**

```json
// internal/adapter/testdata/clippy_sample.json
{"reason":"compiler-artifact"}
{"reason":"compiler-message","message":{"level":"warning","code":{"code":"clippy::bool_comparison"},"message":"equality checks against true are unnecessary","spans":[{"file_name":"src/main.rs","line_start":3,"is_primary":true}]}}
{"reason":"compiler-message","message":{"level":"error","code":{"code":"clippy::mutex_atomic"},"message":"consider using an AtomicBool instead of a Mutex<bool>","spans":[{"file_name":"src/lock.rs","line_start":10,"is_primary":true}]}}
```

- [ ] **Step 2: Write the failing test**

```go
// internal/adapter/clippy_test.go
package adapter

import (
	"bytes"
	"os"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestClippy_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/clippy_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := clippyFindings(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(findings))
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
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/adapter/... -run TestClippy_parse`
Expected: FAIL — `clippyFindings` undefined

- [ ] **Step 4: Write minimal implementation**

```go
// internal/adapter/clippy.go
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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/adapter/... -run TestClippy_parse`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/clippy.go internal/adapter/clippy_test.go internal/adapter/testdata/clippy_sample.json
git commit -m "feat: add clippy adapter"
```

---

### Task 8: `cargo-audit` adapter

**Files:**
- Create: `internal/adapter/cargoaudit.go`
- Test: `internal/adapter/cargoaudit_test.go`
- Test data: `internal/adapter/testdata/cargoaudit_sample.json`

**Interfaces:**
- Consumes: `ToolAdapter`, `finding.Finding` from [[adapter]]/[[finding]].
- Produces: `CargoAudit{}` implementing `ToolAdapter`.

- [ ] **Step 1: Author the sample fixture**

```json
// internal/adapter/testdata/cargoaudit_sample.json
{
  "vulnerabilities": {
    "found": true,
    "list": [
      {
        "advisory": {"id": "RUSTSEC-2021-0001", "title": "Use-after-free in vulnerable-crate"},
        "package": {"name": "vulnerable-crate"}
      }
    ]
  }
}
```

- [ ] **Step 2: Write the failing test**

```go
// internal/adapter/cargoaudit_test.go
package adapter

import (
	"encoding/json"
	"os"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestCargoAudit_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/cargoaudit_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed cargoAuditOutput
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	findings := cargoAuditFindings(parsed)

	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	f := findings[0]
	if f.RuleID != "RUSTSEC-2021-0001" || f.File != "Cargo.lock" {
		t.Errorf("finding = %+v", f)
	}
	if f.Category != finding.CategorySecurity || f.Severity != finding.SeverityHigh {
		t.Errorf("category/severity = %v/%v", f.Category, f.Severity)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/adapter/... -run TestCargoAudit_parse`
Expected: FAIL — `cargoAuditOutput`/`cargoAuditFindings` undefined

- [ ] **Step 4: Write minimal implementation**

```go
// internal/adapter/cargoaudit.go
package adapter

import (
	"encoding/json"
	"fmt"
	"os/exec"

	"codebase-analyser/internal/finding"
)

type CargoAudit struct{}

func (CargoAudit) Name() string         { return "cargo-audit" }
func (CargoAudit) CheckInstalled() bool { return commandExists("cargo-audit") }

func (CargoAudit) Install() error {
	return exec.Command("cargo", "install", "cargo-audit").Run()
}

type cargoAuditOutput struct {
	Vulnerabilities struct {
		List []struct {
			Advisory struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"advisory"`
			Package struct {
				Name string `json:"name"`
			} `json:"package"`
		} `json:"list"`
	} `json:"vulnerabilities"`
}

func (CargoAudit) Run(path string) ([]finding.Finding, error) {
	out, err := runCommand(path, "cargo", "audit", "--json")
	if err != nil {
		return nil, fmt.Errorf("cargo-audit: %w", err)
	}
	var parsed cargoAuditOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("cargo-audit: parsing output: %w", err)
	}
	return cargoAuditFindings(parsed), nil
}

func cargoAuditFindings(parsed cargoAuditOutput) []finding.Finding {
	findings := make([]finding.Finding, 0, len(parsed.Vulnerabilities.List))
	for _, v := range parsed.Vulnerabilities.List {
		// ponytail: cargo-audit's JSON doesn't reliably carry a severity level;
		// every known-CVE dependency defaults to "high" until a CVSS parser is added.
		findings = append(findings, finding.Finding{
			File:     "Cargo.lock",
			Line:     0,
			Tool:     "cargo-audit",
			RuleID:   v.Advisory.ID,
			Category: finding.CategorySecurity,
			Severity: finding.SeverityHigh,
			Message:  fmt.Sprintf("%s (%s)", v.Advisory.Title, v.Package.Name),
		})
	}
	return findings
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/adapter/... -run TestCargoAudit_parse`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/cargoaudit.go internal/adapter/cargoaudit_test.go internal/adapter/testdata/cargoaudit_sample.json
git commit -m "feat: add cargo-audit adapter"
```

---

### Task 9: `orchestrator` package

**Files:**
- Create: `internal/orchestrator/orchestrator.go`
- Test: `internal/orchestrator/orchestrator_test.go`

**Interfaces:**
- Consumes: `detect.Project` from [[detect]], `adapter.ToolAdapter` from [[adapter]], `finding.Finding` from [[finding]].
- Produces: `ToolResult{Tool string, Findings []finding.Finding, Skipped bool, Error error}`, `DefaultAdapters map[string][]adapter.ToolAdapter` (keys `"go"`, `"rust"`), `Run(projects []detect.Project, adaptersByLang map[string][]adapter.ToolAdapter) []ToolResult`. [[cli]] calls `Run` with `DefaultAdapters`.

- [ ] **Step 1: Write the failing test (uses fake adapters — no real tools required)**

```go
// internal/orchestrator/orchestrator_test.go
package orchestrator

import (
	"errors"
	"testing"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/detect"
	"codebase-analyser/internal/finding"
)

type fakeAdapter struct {
	name      string
	installed bool
	findings  []finding.Finding
	runErr    error
}

func (f fakeAdapter) Name() string         { return f.name }
func (f fakeAdapter) CheckInstalled() bool { return f.installed }
func (f fakeAdapter) Install() error       { return nil }
func (f fakeAdapter) Run(path string) ([]finding.Finding, error) {
	return f.findings, f.runErr
}

func TestRun_collectsFindingsAndSkips(t *testing.T) {
	projects := []detect.Project{{Path: "/repo", Language: "go"}}
	adapters := map[string][]adapter.ToolAdapter{
		"go": {
			fakeAdapter{name: "ok-tool", installed: true, findings: []finding.Finding{{Tool: "ok-tool", RuleID: "R1"}}},
			fakeAdapter{name: "broken-tool", installed: true, runErr: errors.New("boom")},
		},
	}

	results := Run(projects, adapters)

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	var okResult, brokenResult ToolResult
	for _, r := range results {
		switch r.Tool {
		case "ok-tool":
			okResult = r
		case "broken-tool":
			brokenResult = r
		}
	}
	if okResult.Skipped || len(okResult.Findings) != 1 {
		t.Errorf("ok-tool result = %+v", okResult)
	}
	if !brokenResult.Skipped || brokenResult.Error == nil {
		t.Errorf("broken-tool result = %+v, want Skipped=true with Error", brokenResult)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/orchestrator/...`
Expected: FAIL — package doesn't exist yet

- [ ] **Step 3: Write minimal implementation**

```go
// internal/orchestrator/orchestrator.go
package orchestrator

import (
	"fmt"
	"os"
	"sync"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/detect"
	"codebase-analyser/internal/finding"
)

type ToolResult struct {
	Tool     string
	Findings []finding.Finding
	Skipped  bool
	Error    error
}

var DefaultAdapters = map[string][]adapter.ToolAdapter{
	"go":   {adapter.GolangciLint{}, adapter.Gosec{}, adapter.Govulncheck{}},
	"rust": {adapter.Clippy{}, adapter.CargoAudit{}},
}

// Run executes every adapter for every detected project concurrently. Each
// adapter's timeout is enforced inside its own Run call (see adapter.DefaultTimeout);
// a crashing or erroring adapter is recorded as skipped and never blocks the others.
func Run(projects []detect.Project, adaptersByLang map[string][]adapter.ToolAdapter) []ToolResult {
	var wg sync.WaitGroup
	resultsCh := make(chan ToolResult)

	for _, p := range projects {
		for _, a := range adaptersByLang[p.Language] {
			wg.Add(1)
			go func(a adapter.ToolAdapter, path string) {
				defer wg.Done()
				resultsCh <- runOne(a, path)
			}(a, p.Path)
		}
	}

	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	var results []ToolResult
	for r := range resultsCh {
		results = append(results, r)
	}
	return results
}

func runOne(a adapter.ToolAdapter, path string) ToolResult {
	if !a.CheckInstalled() {
		fmt.Fprintf(os.Stderr, "installing %s...\n", a.Name())
		if err := a.Install(); err != nil {
			return ToolResult{Tool: a.Name(), Skipped: true, Error: fmt.Errorf("install failed: %w", err)}
		}
	}
	findings, err := a.Run(path)
	if err != nil {
		return ToolResult{Tool: a.Name(), Skipped: true, Error: err}
	}
	return ToolResult{Tool: a.Name(), Findings: findings}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/orchestrator/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator
git commit -m "feat: add concurrent tool orchestrator with per-tool skip handling"
```

---

### Task 10: `explain` package — Explainer interface + batching

**Files:**
- Create: `internal/explain/explain.go`
- Test: `internal/explain/explain_test.go`

**Interfaces:**
- Consumes: `finding.Finding`, `finding.ExplainedFinding` from [[finding]].
- Produces: `Explanation{Text, FixPattern string}`, `Explainer` interface (`Explain(ctx context.Context, tool, ruleID, sampleMessage string, count int) (Explanation, error)`), `Group(ctx context.Context, e Explainer, findings []finding.Finding) []finding.ExplainedFinding`.

- [ ] **Step 1: Write the failing test**

```go
// internal/explain/explain_test.go
package explain

import (
	"context"
	"errors"
	"testing"

	"codebase-analyser/internal/finding"
)

type stubExplainer struct {
	calls int
	fail  map[string]bool // ruleID -> should fail
}

func (s *stubExplainer) Explain(ctx context.Context, tool, ruleID, sampleMessage string, count int) (Explanation, error) {
	s.calls++
	if s.fail[ruleID] {
		return Explanation{}, errors.New("llm down")
	}
	return Explanation{Text: "why: " + ruleID, FixPattern: "fix: " + ruleID}, nil
}

func TestGroup_batchesPerToolAndRule(t *testing.T) {
	findings := []finding.Finding{
		{Tool: "gosec", RuleID: "G101", Message: "m1"},
		{Tool: "gosec", RuleID: "G101", Message: "m2"},
		{Tool: "gosec", RuleID: "G104", Message: "m3"},
	}
	stub := &stubExplainer{fail: map[string]bool{}}

	out := Group(context.Background(), stub, findings)

	if stub.calls != 2 {
		t.Fatalf("got %d Explain calls, want 2 (one per rule)", stub.calls)
	}
	if len(out) != 3 {
		t.Fatalf("got %d explained findings, want 3", len(out))
	}
	for _, f := range out {
		if f.Explanation == "" {
			t.Errorf("finding %+v missing explanation", f)
		}
	}
}

func TestGroup_llmFailureFallsBackToUnexplained(t *testing.T) {
	findings := []finding.Finding{{Tool: "gosec", RuleID: "G101", Message: "m1"}}
	stub := &stubExplainer{fail: map[string]bool{"G101": true}}

	out := Group(context.Background(), stub, findings)

	if len(out) != 1 || out[0].Explanation != "" {
		t.Fatalf("got %+v, want one finding with no explanation", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/explain/...`
Expected: FAIL — package doesn't exist yet

- [ ] **Step 3: Write minimal implementation**

```go
// internal/explain/explain.go
package explain

import (
	"context"

	"codebase-analyser/internal/finding"
)

type Explanation struct {
	Text       string
	FixPattern string
}

type Explainer interface {
	Explain(ctx context.Context, tool, ruleID, sampleMessage string, count int) (Explanation, error)
}

// Group groups findings by (tool, ruleID) and calls e.Explain once per group,
// attaching the shared explanation to every finding in it. A group whose call
// fails falls back to unexplained findings rather than blocking the rest.
func Group(ctx context.Context, e Explainer, findings []finding.Finding) []finding.ExplainedFinding {
	type key struct{ tool, rule string }
	groups := map[key][]int{}
	for i, f := range findings {
		k := key{f.Tool, f.RuleID}
		groups[k] = append(groups[k], i)
	}

	explained := finding.WithoutExplanation(findings)

	for k, idxs := range groups {
		exp, err := e.Explain(ctx, k.tool, k.rule, findings[idxs[0]].Message, len(idxs))
		if err != nil {
			continue
		}
		for _, i := range idxs {
			explained[i].Explanation = exp.Text
			explained[i].FixPattern = exp.FixPattern
		}
	}
	return explained
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/explain/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/explain/explain.go internal/explain/explain_test.go
git commit -m "feat: add Explainer interface and per-rule batching"
```

---

### Task 11: LLM providers (Anthropic, OpenAI, Gemini) + selection

**Files:**
- Create: `internal/explain/providers.go`
- Create: `internal/explain/anthropic.go`
- Create: `internal/explain/openai.go`
- Create: `internal/explain/gemini.go`
- Test: `internal/explain/providers_test.go`

**Interfaces:**
- Consumes: `Explainer`, `Explanation` from [[explain]].
- Produces: `AnthropicExplainer{APIKey string, HTTPClient *http.Client, BaseURL string}`, `OpenAIExplainer{...same shape...}`, `GeminiExplainer{...same shape...}` (each implementing `Explainer`), `SelectProvider(flagOverride string, getenv func(string) string) (Explainer, string, bool)`. `BaseURL` empty means the real API; tests set it to an `httptest.Server` URL. [[cli]] calls `SelectProvider(cfg.LLMProvider, os.Getenv)`.

- [ ] **Step 1: Write the failing tests (httptest servers stand in for each API)**

```go
// internal/explain/providers_test.go
package explain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicExplainer_Explain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing/wrong x-api-key header")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"text": "Why it matters: X\nFix pattern: Y"}},
		})
	}))
	defer srv.Close()

	e := &AnthropicExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	exp, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Text != "X" || exp.FixPattern != "Y" {
		t.Errorf("got %+v", exp)
	}
}

func TestOpenAIExplainer_Explain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing/wrong Authorization header")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "Why it matters: X\nFix pattern: Y"}}},
		})
	}))
	defer srv.Close()

	e := &OpenAIExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	exp, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Text != "X" || exp.FixPattern != "Y" {
		t.Errorf("got %+v", exp)
	}
}

func TestGeminiExplainer_Explain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("missing/wrong key query param")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]string{{"text": "Why it matters: X\nFix pattern: Y"}}}}},
		})
	}))
	defer srv.Close()

	e := &GeminiExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	exp, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Text != "X" || exp.FixPattern != "Y" {
		t.Errorf("got %+v", exp)
	}
}

func TestSelectProvider_priorityOrder(t *testing.T) {
	env := map[string]string{"OPENAI_API_KEY": "o-key", "GEMINI_API_KEY": "g-key"}
	getenv := func(k string) string { return env[k] }

	_, provider, ok := SelectProvider("", getenv)
	if !ok || provider != "openai" {
		t.Fatalf("provider = %q, ok = %v; want openai, true", provider, ok)
	}
}

func TestSelectProvider_flagOverridesEnv(t *testing.T) {
	env := map[string]string{"ANTHROPIC_API_KEY": "a-key"}
	getenv := func(k string) string { return env[k] }

	_, provider, ok := SelectProvider("gemini", getenv)
	if !ok || provider != "gemini" {
		t.Fatalf("provider = %q, ok = %v; want gemini, true", provider, ok)
	}
}

func TestSelectProvider_noneConfigured(t *testing.T) {
	getenv := func(k string) string { return "" }
	_, _, ok := SelectProvider("", getenv)
	if ok {
		t.Fatal("expected ok = false when no provider is configured")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/explain/...`
Expected: FAIL — `AnthropicExplainer` etc. undefined

- [ ] **Step 3: Write shared helpers**

```go
// internal/explain/providers.go
package explain

import (
	"fmt"
	"net/http"
	"strings"
)

func buildPrompt(tool, ruleID, sampleMessage string, count int) string {
	return fmt.Sprintf(
		"A static analysis tool (%s, rule %s) flagged %d instance(s) in a production codebase. "+
			"Sample message: %q\n\n"+
			"In two short paragraphs: (1) why this matters in production, (2) a general fix pattern. "+
			"Label them exactly \"Why it matters:\" and \"Fix pattern:\".",
		tool, ruleID, count, sampleMessage)
}

func parseExplanation(text string) Explanation {
	why, fix := text, ""
	if idx := strings.Index(text, "Fix pattern:"); idx >= 0 {
		why = strings.TrimSpace(strings.Replace(text[:idx], "Why it matters:", "", 1))
		fix = strings.TrimSpace(text[idx+len("Fix pattern:"):])
	}
	return Explanation{Text: why, FixPattern: fix}
}

// SelectProvider picks the Explainer to use: an explicit flag wins outright,
// otherwise the first of ANTHROPIC_API_KEY/OPENAI_API_KEY/GEMINI_API_KEY that's set.
func SelectProvider(flagOverride string, getenv func(string) string) (Explainer, string, bool) {
	if flagOverride != "" {
		return newProvider(flagOverride, getenv), flagOverride, true
	}
	for _, p := range []string{"anthropic", "openai", "gemini"} {
		if getenv(envVarFor(p)) != "" {
			return newProvider(p, getenv), p, true
		}
	}
	return nil, "", false
}

func envVarFor(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "gemini":
		return "GEMINI_API_KEY"
	}
	return ""
}

func newProvider(name string, getenv func(string) string) Explainer {
	apiKey := getenv(envVarFor(name))
	switch name {
	case "anthropic":
		return &AnthropicExplainer{APIKey: apiKey, HTTPClient: http.DefaultClient}
	case "openai":
		return &OpenAIExplainer{APIKey: apiKey, HTTPClient: http.DefaultClient}
	case "gemini":
		return &GeminiExplainer{APIKey: apiKey, HTTPClient: http.DefaultClient}
	}
	return nil
}
```

- [ ] **Step 4: Write the Anthropic provider**

```go
// internal/explain/anthropic.go
package explain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type AnthropicExplainer struct {
	APIKey     string
	HTTPClient *http.Client
	BaseURL    string // empty = real API; set in tests to an httptest.Server URL
}

func (a *AnthropicExplainer) Explain(ctx context.Context, tool, ruleID, sampleMessage string, count int) (Explanation, error) {
	url := a.BaseURL
	if url == "" {
		url = "https://api.anthropic.com/v1/messages"
	}
	body, _ := json.Marshal(map[string]any{
		"model":      "claude-sonnet-5",
		"max_tokens": 300,
		"messages":   []map[string]string{{"role": "user", "content": buildPrompt(tool, ruleID, sampleMessage, count)}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Explanation{}, err
	}
	req.Header.Set("x-api-key", a.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return Explanation{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Explanation{}, fmt.Errorf("anthropic: status %d", resp.StatusCode)
	}
	var parsed struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Explanation{}, err
	}
	if len(parsed.Content) == 0 {
		return Explanation{}, fmt.Errorf("anthropic: empty response")
	}
	return parseExplanation(parsed.Content[0].Text), nil
}
```

- [ ] **Step 5: Write the OpenAI provider**

```go
// internal/explain/openai.go
package explain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type OpenAIExplainer struct {
	APIKey     string
	HTTPClient *http.Client
	BaseURL    string
}

func (o *OpenAIExplainer) Explain(ctx context.Context, tool, ruleID, sampleMessage string, count int) (Explanation, error) {
	url := o.BaseURL
	if url == "" {
		url = "https://api.openai.com/v1/chat/completions"
	}
	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o-mini",
		"messages": []map[string]string{{"role": "user", "content": buildPrompt(tool, ruleID, sampleMessage, count)}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Explanation{}, err
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.HTTPClient.Do(req)
	if err != nil {
		return Explanation{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Explanation{}, fmt.Errorf("openai: status %d", resp.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Explanation{}, err
	}
	if len(parsed.Choices) == 0 {
		return Explanation{}, fmt.Errorf("openai: empty response")
	}
	return parseExplanation(parsed.Choices[0].Message.Content), nil
}
```

- [ ] **Step 6: Write the Gemini provider**

```go
// internal/explain/gemini.go
package explain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type GeminiExplainer struct {
	APIKey     string
	HTTPClient *http.Client
	BaseURL    string
}

func (g *GeminiExplainer) Explain(ctx context.Context, tool, ruleID, sampleMessage string, count int) (Explanation, error) {
	base := g.BaseURL
	if base == "" {
		base = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"
	}
	reqURL := base + "?key=" + url.QueryEscape(g.APIKey)

	body, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{{"parts": []map[string]string{{"text": buildPrompt(tool, ruleID, sampleMessage, count)}}}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return Explanation{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return Explanation{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Explanation{}, fmt.Errorf("gemini: status %d", resp.StatusCode)
	}
	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Explanation{}, err
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return Explanation{}, fmt.Errorf("gemini: empty response")
	}
	return parseExplanation(parsed.Candidates[0].Content.Parts[0].Text), nil
}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/explain/...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/explain/providers.go internal/explain/anthropic.go internal/explain/openai.go internal/explain/gemini.go internal/explain/providers_test.go
git commit -m "feat: add Anthropic/OpenAI/Gemini providers and auto-detection"
```

---

### Task 12: `report` package — human + JSON renderers

**Files:**
- Create: `internal/report/human.go`
- Create: `internal/report/json.go`
- Test: `internal/report/human_test.go`
- Test: `internal/report/json_test.go`

**Interfaces:**
- Consumes: `finding.ExplainedFinding`, `finding.Category*`, `finding.Severity*` from [[finding]].
- Produces: `Summary(findings []finding.ExplainedFinding) map[finding.Severity]int`, `RenderHuman(w io.Writer, findings []finding.ExplainedFinding)`, `RenderJSON(w io.Writer, findings []finding.ExplainedFinding) error`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/report/human_test.go
package report

import (
	"bytes"
	"strings"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestRenderHuman(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{File: "bad.go", Line: 6, Tool: "gosec", RuleID: "G101",
			Category: finding.CategorySecurity, Severity: finding.SeverityCritical, Message: "hardcoded credential"},
			Explanation: "secrets in source get leaked via git history"},
	}
	var buf bytes.Buffer
	RenderHuman(&buf, findings)
	out := buf.String()

	if !strings.Contains(out, "critical=1") {
		t.Errorf("summary missing critical=1: %s", out)
	}
	if !strings.Contains(out, "security") {
		t.Errorf("missing category header: %s", out)
	}
	if !strings.Contains(out, "bad.go:6") {
		t.Errorf("missing file:line: %s", out)
	}
	if !strings.Contains(out, "secrets in source get leaked") {
		t.Errorf("missing explanation: %s", out)
	}
}

func TestSummary(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{Severity: finding.SeverityHigh}},
		{Finding: finding.Finding{Severity: finding.SeverityHigh}},
		{Finding: finding.Finding{Severity: finding.SeverityLow}},
	}
	counts := Summary(findings)
	if counts[finding.SeverityHigh] != 2 || counts[finding.SeverityLow] != 1 {
		t.Errorf("got %+v", counts)
	}
}
```

```go
// internal/report/json_test.go
package report

import (
	"bytes"
	"encoding/json"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestRenderJSON(t *testing.T) {
	findings := []finding.ExplainedFinding{
		{Finding: finding.Finding{File: "bad.go", Line: 6, Tool: "gosec", RuleID: "G101",
			Category: finding.CategorySecurity, Severity: finding.SeverityCritical, Message: "hardcoded credential"},
			Explanation: "why"},
	}
	var buf bytes.Buffer
	if err := RenderJSON(&buf, findings); err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Summary  map[string]int `json:"summary"`
		Findings []struct {
			File        string `json:"file"`
			Line        int    `json:"line"`
			RuleID      string `json:"ruleID"`
			Explanation string `json:"explanation"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Summary["critical"] != 1 {
		t.Errorf("summary.critical = %d, want 1", parsed.Summary["critical"])
	}
	if len(parsed.Findings) != 1 || parsed.Findings[0].RuleID != "G101" || parsed.Findings[0].Explanation != "why" {
		t.Errorf("findings = %+v", parsed.Findings)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/report/...`
Expected: FAIL — package doesn't exist yet

- [ ] **Step 3: Write minimal implementation**

```go
// internal/report/human.go
package report

import (
	"fmt"
	"io"

	"codebase-analyser/internal/finding"
)

func Summary(findings []finding.ExplainedFinding) map[finding.Severity]int {
	counts := map[finding.Severity]int{}
	for _, f := range findings {
		counts[f.Severity]++
	}
	return counts
}

var categoryOrder = []finding.Category{
	finding.CategoryCorrectness, finding.CategoryConcurrency, finding.CategorySecurity, finding.CategoryOperational,
}
var severityOrder = []finding.Severity{
	finding.SeverityCritical, finding.SeverityHigh, finding.SeverityMedium, finding.SeverityLow,
}

func RenderHuman(w io.Writer, findings []finding.ExplainedFinding) {
	counts := Summary(findings)
	fmt.Fprintf(w, "Summary: critical=%d high=%d medium=%d low=%d\n\n",
		counts[finding.SeverityCritical], counts[finding.SeverityHigh],
		counts[finding.SeverityMedium], counts[finding.SeverityLow])

	byCategory := map[finding.Category][]finding.ExplainedFinding{}
	for _, f := range findings {
		byCategory[f.Category] = append(byCategory[f.Category], f)
	}

	for _, category := range categoryOrder {
		group := byCategory[category]
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(w, "== %s ==\n", category)
		renderCategory(w, group)
		fmt.Fprintln(w)
	}
}

func renderCategory(w io.Writer, group []finding.ExplainedFinding) {
	bySeverity := map[finding.Severity][]finding.ExplainedFinding{}
	for _, f := range group {
		bySeverity[f.Severity] = append(bySeverity[f.Severity], f)
	}
	for _, sev := range severityOrder {
		sevGroup := bySeverity[sev]
		if len(sevGroup) == 0 {
			continue
		}
		renderRules(w, sev, sevGroup)
	}
}

func renderRules(w io.Writer, sev finding.Severity, sevGroup []finding.ExplainedFinding) {
	byRule := map[string][]finding.ExplainedFinding{}
	var ruleOrder []string
	for _, f := range sevGroup {
		key := f.Tool + "/" + f.RuleID
		if _, seen := byRule[key]; !seen {
			ruleOrder = append(ruleOrder, key)
		}
		byRule[key] = append(byRule[key], f)
	}
	for _, rule := range ruleOrder {
		items := byRule[rule]
		fmt.Fprintf(w, "  [%s] %s (%d)\n", sev, rule, len(items))
		if items[0].Explanation != "" {
			fmt.Fprintf(w, "    %s\n", items[0].Explanation)
		}
		for _, f := range items {
			fmt.Fprintf(w, "    - %s:%d %s\n", f.File, f.Line, f.Message)
		}
	}
}
```

```go
// internal/report/json.go
package report

import (
	"encoding/json"
	"io"

	"codebase-analyser/internal/finding"
)

type jsonFinding struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Tool        string `json:"tool"`
	RuleID      string `json:"ruleID"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
	Explanation string `json:"explanation"`
}

type jsonReport struct {
	Summary  map[string]int `json:"summary"`
	Findings []jsonFinding  `json:"findings"`
}

func RenderJSON(w io.Writer, findings []finding.ExplainedFinding) error {
	counts := Summary(findings)
	out := jsonReport{
		Summary: map[string]int{
			"critical": counts[finding.SeverityCritical],
			"high":     counts[finding.SeverityHigh],
			"medium":   counts[finding.SeverityMedium],
			"low":      counts[finding.SeverityLow],
		},
	}
	for _, f := range findings {
		out.Findings = append(out.Findings, jsonFinding{
			File: f.File, Line: f.Line, Tool: f.Tool, RuleID: f.RuleID,
			Category: string(f.Category), Severity: string(f.Severity),
			Message: f.Message, Explanation: f.Explanation,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/report/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/report
git commit -m "feat: add human and JSON report renderers"
```

---

### Task 13: `cli` package — wire the pipeline + exit code

**Files:**
- Create: `internal/cli/run.go`
- Test: `internal/cli/run_test.go`
- Modify: `cmd/analyser/main.go`

**Interfaces:**
- Consumes: `detect.Detect` [[detect]], `orchestrator.Run`/`orchestrator.DefaultAdapters` [[orchestrator]], `explain.SelectProvider`/`explain.Group` [[explain]], `report.RenderHuman`/`report.RenderJSON` [[report]], `finding.*` [[finding]].
- Produces: `RunConfig{Path, Format string, Severity finding.Severity, Categories []finding.Category, LLMProvider string, NoLLM bool}`, `Execute(ctx context.Context, w io.Writer, cfg RunConfig) (exitCode int, err error)`, `NewRunCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing test (drives `Execute` directly, bypassing cobra and real tools via a stub-adapter round trip is out of scope here — this test exercises detection-failure and no-LLM/category-filter wiring, which don't need real tools)**

```go
// internal/cli/run_test.go
package cli

import (
	"bytes"
	"context"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/...`
Expected: FAIL — package doesn't exist yet

- [ ] **Step 3: Write minimal implementation**

```go
// internal/cli/run.go
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

func NewRunCmd() *cobra.Command {
	cfg := RunConfig{}
	var severityFlag string
	var categoryFlags []string

	cmd := &cobra.Command{
		Use:   "run <path>",
		Short: "Analyse a Go/Rust codebase for production-safety issues",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Path = args[0]
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
				os.Exit(exitCode)
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
func Execute(ctx context.Context, w io.Writer, cfg RunConfig) (int, error) {
	projects, err := detect.Detect(cfg.Path)
	if err != nil {
		return 1, err
	}
	if len(projects) == 0 {
		return 1, fmt.Errorf("no Go or Rust project found under %s", cfg.Path)
	}

	results := orchestrator.Run(projects, orchestrator.DefaultAdapters)
	var findings []finding.Finding
	for _, r := range results {
		if r.Skipped {
			fmt.Fprintf(w, "note: skipped %s: %v\n", r.Tool, r.Error)
			continue
		}
		findings = append(findings, r.Findings...)
	}
	if len(cfg.Categories) > 0 {
		findings = filterCategories(findings, cfg.Categories)
	}

	explained := resolveExplanations(ctx, w, cfg, findings)

	if cfg.Format == "json" {
		if err := report.RenderJSON(w, explained); err != nil {
			return 1, err
		}
	} else {
		report.RenderHuman(w, explained)
	}

	for _, f := range explained {
		if finding.MeetsThreshold(f.Severity, cfg.Severity) {
			return 1, nil
		}
	}
	return 0, nil
}

func resolveExplanations(ctx context.Context, w io.Writer, cfg RunConfig, findings []finding.Finding) []finding.ExplainedFinding {
	if cfg.NoLLM {
		return finding.WithoutExplanation(findings)
	}
	explainer, _, ok := explain.SelectProvider(cfg.LLMProvider, os.Getenv)
	if !ok {
		fmt.Fprintln(w, "note: no LLM provider configured (set ANTHROPIC_API_KEY, OPENAI_API_KEY, or GEMINI_API_KEY); showing raw findings only")
		return finding.WithoutExplanation(findings)
	}
	return explain.Group(ctx, explainer, findings)
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
```

- [ ] **Step 4: Wire it into main.go**

```go
// cmd/analyser/main.go
package main

import (
	"os"

	"github.com/spf13/cobra"

	"codebase-analyser/internal/cli"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "analyser",
		Short: "Analyse Go/Rust codebases for production-safety issues",
	}
	root.AddCommand(cli.NewRunCmd())
	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./... `
Expected: PASS across all packages

- [ ] **Step 6: Commit**

```bash
git add internal/cli cmd/analyser/main.go
git commit -m "feat: wire detect/orchestrator/explain/report into analyser run"
```

---

### Task 14: End-to-end smoke test with fixture repos

**Files:**
- Create: `testdata/fixtures/go-repo/go.mod`
- Create: `testdata/fixtures/go-repo/bad.go`
- Create: `testdata/fixtures/rust-repo/Cargo.toml`
- Create: `testdata/fixtures/rust-repo/src/main.rs`
- Test: `e2e_test.go` (repo root, package `main_test` via `_test` build — placed as its own package `e2e`)

**Interfaces:**
- Consumes: `cli.Execute`, `cli.RunConfig` from [[cli]].
- Produces: nothing further downstream; this is the final validation task.

- [ ] **Step 1: Author the fixtures**

```go
// testdata/fixtures/go-repo/go.mod
module fixture

go 1.22
```

```go
// testdata/fixtures/go-repo/bad.go
package fixture

import "os"

func readSecret() string {
	apiKey := "sk-1234567890abcdef1234567890abcdef" // gosec G101: hardcoded credential
	os.Open("/tmp/x")                                // errcheck: ignored error
	return apiKey
}
```

```toml
# testdata/fixtures/rust-repo/Cargo.toml
[package]
name = "fixture"
version = "0.1.0"
edition = "2021"
```

```rust
// testdata/fixtures/rust-repo/src/main.rs
fn main() {
    let x = true;
    if x == true {
        println!("yes");
    }
}
```

- [ ] **Step 2: Write the failing test**

```go
// e2e_test.go
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"codebase-analyser/internal/cli"
	"codebase-analyser/internal/finding"
)

func requireTool(t *testing.T, name string) {
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not installed, skipping e2e smoke test", name)
	}
}

func TestEndToEnd_findsKnownIssues(t *testing.T) {
	requireTool(t, "golangci-lint")
	requireTool(t, "gosec")
	requireTool(t, "cargo")

	var buf bytes.Buffer
	_, err := cli.Execute(context.Background(), &buf, cli.RunConfig{
		Path:     "testdata/fixtures",
		Format:   "json",
		Severity: finding.SeverityLow,
		NoLLM:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Findings []struct {
			RuleID string `json:"ruleID"`
			Tool   string `json:"tool"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, buf.String())
	}

	var ruleIDs []string
	for _, f := range parsed.Findings {
		ruleIDs = append(ruleIDs, f.Tool+":"+f.RuleID)
	}
	joined := strings.Join(ruleIDs, ",")
	if !strings.Contains(joined, "gosec:G101") {
		t.Errorf("expected gosec:G101 (hardcoded credential) in findings, got %s", joined)
	}
}
```

- [ ] **Step 3: Run test to verify it fails (or skips cleanly without tools)**

Run: `go test . -run TestEndToEnd_findsKnownIssues -v`
Expected: FAIL before fixtures/wiring exist, or SKIP with a clear message if `golangci-lint`/`gosec`/`cargo` aren't on PATH

- [ ] **Step 4: No new production code needed** — this task validates Tasks 1–13 together via the fixtures. If it fails once the tools are installed, the failure points at a specific adapter or wiring bug to fix in its own task's file.

- [ ] **Step 5: Run full suite**

Run: `go build ./... && go test ./...`
Expected: PASS (or documented SKIP for the e2e test on machines without the wrapped tools installed)

- [ ] **Step 6: Commit**

```bash
git add testdata/fixtures e2e_test.go
git commit -m "test: add end-to-end smoke test with Go/Rust fixture repos"
```

---

## Post-plan notes

- `ponytail:` comments mark three deliberate v1 simplifications: golangci-lint/clippy category-and-severity mappings are heuristic tables, govulncheck reports only the last resolved trace position, and cargo-audit findings default to `high` severity absent a CVSS parser. Each names its own upgrade path inline; no separate tracking needed unless `ponytail-debt` flags them.
- Per the spec's non-goals, this plan does not add a config file, CI integration, or a `--race` flag — flag it back to the spec if scope creeps during implementation.
