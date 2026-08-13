# Codebase Analyser MCP Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `codebase-analyser-mcp`, an MCP server over stdio that exposes the existing analyser pipeline as `analyze_codebase` and `push_to_dashboard` tools to any MCP-compatible agent (Claude Code, Codex CLI, Gemini CLI).

**Architecture:** A second front door on the engine that already exists. `internal/mcpserver` calls `detect.Detect` → `orchestrator.Run` directly — the same two calls `internal/cli` makes — and marshals the result into MCP tool output. No LLM in this path (the caller is already an LLM). No changes to `detect`, `orchestrator`, `adapter`, or `report` in Stage 1; later stages add one exported helper and one env hook to `adapter`, and one exported helper to `finding`.

**Tech Stack:** Go 1.26.5, `github.com/modelcontextprotocol/go-sdk` v1.7.0 (official Go MCP SDK — stdio transport, generic `AddTool` with automatic JSON-Schema inference, `NewInMemoryTransports` for tests). Stdlib `net/http` for the dashboard push. Node (no npm dependencies) for the distribution wrapper.

**Spec:** [docs/superpowers/specs/2026-08-13-mcp-server-design.md](../specs/2026-08-13-mcp-server-design.md)

---

## Execution: parallel low-cost agents

Dispatch one fresh subagent per task. Use a **low-cost tier** for every task — the code is fully specified below, so the executor is transcribing and testing, not designing. Tier per task is given in each task header (`haiku` = fully mechanical, code is verbatim in the plan; `sonnet` = needs judgement about existing code it must read first).

**Dependency graph — anything on the same line runs in parallel:**

```
Stage 1 (working MCP server — do this first, it is the deliverable)
  Task 1                     (nothing depends on it existing yet — start here)
  Task 2 ‖ Task 3            (both depend on Task 1 only; disjoint files)
  Task 4                     (depends on 2 and 3)
Stage 2 (incremental cache)
  Task 5                     (independent of Stage 1 — may run in parallel with Task 1)
  Task 6                     (depends on 5, and on Task 1 for the call site)
Stage 3 (toolchain)
  Task 7                     (independent of Stages 1-2 — may run in parallel with Task 1)
  Task 8 ‖ Task 9            (8 depends on 7; 9 depends on 7 AND on Task 5,
                              whose cache.Root it reuses for the download dir)
Stage 4 (distribution)
  Task 10                    (depends on Task 1: the binary must build)
  Task 11                    (depends on 10: consumes its release-asset naming)
```

**Stage 1 is the priority.** It produces a server an agent can actually configure and call. Stages 2–4 are performance, portability and packaging on top of a thing that already works — do not start them until Stage 1 is green, except for Tasks 5 and 7, which touch entirely disjoint files and may be handed to idle agents early.

Two agents must never edit the same file concurrently. The only files touched by more than one task are `go.mod`/`go.sum` (Task 1 only), `internal/mcpserver/server.go` (Tasks 1, 2, 3 — strictly ordered above), and `internal/adapter/adapter.go` (Tasks 6 and 7 — different stages, never concurrent).

## Global Constraints

- **Version control is the user's, and every task is a commit checkpoint.** No task runs `git add`, `git commit`, `git init`, or any other mutating git command. A task is done when `go build ./... && go test ./...` is green. Then: report what changed, emit **one single-line commit message** on its own line, and **stop**. The user commits and pushes it themselves; the next task does not start until they say it is pushed. (Read-only `git remote get-url` / `git rev-parse` invocations *inside product code* are fine — that is the dashboard feature, not version control.)
- Module path is `codebase-analyser`, Go 1.26.5. Import internal packages as `codebase-analyser/internal/<pkg>`.
- Binary name is `codebase-analyser-mcp` (spec: Architecture), so its directory is `cmd/codebase-analyser-mcp/`.
- **stdout is the MCP transport.** Any diagnostic written to stdout corrupts the JSON-RPC stream. Everything diagnostic goes to stderr. `orchestrator`'s installer already writes `installing %s...` to stderr — leave it there.
- **No LLM anywhere in this path** (spec: Tools exposed). Do not import `internal/explain`. Findings are returned raw; the calling agent explains them.
- `Category` ∈ `correctness | concurrency | security | operational`; `Severity` ∈ `critical | high | medium | low`. Parse user input through the existing `finding.ParseCategory` / `finding.ParseSeverity` — never re-validate by hand.
- Response cap defaults to **50 findings** (spec: "top ~50"), with accurate totals for everything, always.
- The existing 5-minute per-tool timeout (`adapter.DefaultTimeout`) stays the ceiling. Tool calls block; no background-job/poll split (spec: Reliability).
- Reuse before writing: `detect.Detect`, `orchestrator.Run`, `orchestrator.DefaultAdapters`, `report.RenderJSON`, `report.SkippedTool`, `finding.WithoutExplanation`, `finding.ParseSeverity`, `finding.ParseCategory`, `finding.MeetsThreshold`.
- **Cache key substitution (Stage 2):** the spec says the cache is keyed by "repo path and tool version". Implemented as repo path + the tool binary's `(size, mtime)` rather than a parsed `--version` string. Same guarantee — a replaced binary invalidates the entry — without adding a `Version()` method to `ToolAdapter` and its five implementations. Do not add that method.
- **Toolchain mechanism (Stage 3):** Go and Rust already ship the machinery for "run the version this repo declares" — `GOTOOLCHAIN` and `RUSTUP_TOOLCHAIN`. Tasks 7–8 use those. Task 9 adds the download-our-own path for machines with no Go/Rust at all. Do not write a downloader in Task 7 or 8.

---

## File Structure

```
codebase-analyser/
├── cmd/
│   ├── analyser/                      # existing CLI, untouched
│   └── codebase-analyser-mcp/
│       └── main.go                    # ~15 lines: build server, Run on stdio      [Task 1]
├── internal/
│   ├── mcpserver/
│   │   ├── server.go                  # Server struct, New(), tool registration    [Tasks 1,2,3]
│   │   ├── analyze.go                 # analyze_codebase input/output + handler    [Tasks 1,2]
│   │   ├── analyze_test.go            #                                            [Tasks 1,2]
│   │   ├── dashboard.go               # push_to_dashboard handler + HTTP POST      [Task 3]
│   │   └── dashboard_test.go          #                                            [Task 3]
│   ├── cache/
│   │   ├── cache.go                   # Fingerprint, Store (get/put)               [Task 5]
│   │   ├── cache_test.go              #                                            [Task 5]
│   │   ├── packages.go                # enumerate Go packages / Rust crates        [Task 6]
│   │   └── packages_test.go           #                                            [Task 6]
│   ├── toolchain/
│   │   ├── toolchain.go               # Resolver interface + Env(path)             [Task 7]
│   │   ├── golang.go / golang_test.go # go.mod detection -> GOTOOLCHAIN            [Task 7]
│   │   ├── rust.go / rust_test.go     # rust-toolchain.toml -> RUSTUP_TOOLCHAIN    [Task 8]
│   │   └── bootstrap.go / _test.go     # download Go/rustup when absent            [Task 9]
│   ├── adapter/adapter.go             # + ResolveCommand (Task 6), + EnvForPath (Task 7)
│   └── finding/finding.go             # + SeverityRank                             [Task 2]
├── npm/
│   ├── package.json                   # @codebase-analyser/mcp wrapper             [Task 11]
│   ├── index.js                       # platform detect, download, cache, exec     [Task 11]
│   └── index.test.js                  # node:test, no deps                         [Task 11]
├── .github/workflows/release.yml      # 5-target cross-compile + GH release        [Task 10]
├── docs/mcp-server.md                 # config snippets for the three hosts        [Task 4]
└── mcp_e2e_test.go                    # package e2e: real stdio JSON-RPC smoke     [Task 4]
```

---

## Stage 1 — Working MCP server

### Task 1: MCP server + `analyze_codebase` — tier: `sonnet`

**Files:**
- Create: `internal/mcpserver/server.go`
- Create: `internal/mcpserver/analyze.go`
- Create: `internal/mcpserver/analyze_test.go`
- Create: `cmd/codebase-analyser-mcp/main.go`
- Modify: `go.mod`, `go.sum` (add the MCP SDK)

**Interfaces:**
- Consumes: `detect.Detect(root string) ([]detect.Project, []string, error)`; `orchestrator.Run(projects []detect.Project, adaptersByLang map[string][]adapter.ToolAdapter) []orchestrator.ToolResult`; `orchestrator.DefaultAdapters`; `orchestrator.ToolResult{Tool, Path string; Findings []finding.Finding; Skipped bool; Error error}`.
- Produces: `mcpserver.New(adapters map[string][]adapter.ToolAdapter) *Server`; `(*Server).MCP() *mcp.Server`; types `AnalyzeInput`, `AnalyzeOutput`, `Finding`, `SkippedTool`. Task 2 adds fields to `AnalyzeInput`/`AnalyzeOutput` and a `maxFindings` field to `Server`. Task 3 adds a `push_to_dashboard` tool and a `last` field to `Server`.

- [ ] **Step 1: Add the SDK dependency**

Run:
```bash
go get github.com/modelcontextprotocol/go-sdk@v1.7.0
```
Expected: `go.mod` gains `github.com/modelcontextprotocol/go-sdk v1.7.0` plus its transitive requirements (`github.com/google/jsonschema-go`, `github.com/segmentio/encoding`, `github.com/yosida95/uritemplate/v3`, `golang.org/x/oauth2`, `golang.org/x/sync`, `golang.org/x/time`). This is expected and accepted — the spec chose an SDK over hand-rolled JSON-RPC.

- [ ] **Step 2: Write the failing test**

Create `internal/mcpserver/analyze_test.go`:

```go
package mcpserver_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/finding"
	"codebase-analyser/internal/mcpserver"
)

var errFake = errors.New("fake tool failure")

// fakeAdapter stands in for a real linter so tests never shell out.
type fakeAdapter struct {
	name     string
	findings []finding.Finding
	err      error
}

func (f fakeAdapter) Name() string         { return f.name }
func (f fakeAdapter) CheckInstalled() bool { return true }
func (f fakeAdapter) Install() error       { return nil }
func (f fakeAdapter) Run(path string) ([]finding.Finding, error) {
	return f.findings, f.err
}

// goRepo writes a minimal Go project so detect.Detect finds exactly one.
func goRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// connect wires a client to s over an in-memory transport. The server must be
// connected before the client: the client initializes the session on connect.
func connect(t *testing.T, s *mcpserver.Server) *mcp.ClientSession {
	t.Helper()
	ctx := t.Context()
	clientT, serverT := mcp.NewInMemoryTransports()
	ss, err := s.MCP().Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })

	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	cs, err := c.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// callAnalyze calls analyze_codebase and decodes its structured output.
func callAnalyze(t *testing.T, cs *mcp.ClientSession, args map[string]any) (mcpserver.AnalyzeOutput, *mcp.CallToolResult) {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "analyze_codebase", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var out mcpserver.AnalyzeOutput
	if res.StructuredContent != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal structured content: %v", err)
		}
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("decode structured content: %v", err)
		}
	}
	return out, res
}

func TestAnalyzeReturnsFindings(t *testing.T) {
	dir := goRepo(t)
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "fake", findings: []finding.Finding{
			{File: "a.go", Line: 3, Tool: "fake", RuleID: "R1", Category: finding.CategorySecurity, Severity: finding.SeverityCritical, Message: "boom"},
			{File: "b.go", Line: 7, Tool: "fake", RuleID: "R2", Category: finding.CategoryCorrectness, Severity: finding.SeverityLow, Message: "meh"},
		}}},
	})

	out, res := callAnalyze(t, connect(t, s), map[string]any{"path": dir})

	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	if out.Total != 2 {
		t.Errorf("Total = %d, want 2", out.Total)
	}
	if len(out.Findings) != 2 {
		t.Fatalf("len(Findings) = %d, want 2", len(out.Findings))
	}
	if out.Summary["critical"] != 1 || out.Summary["low"] != 1 {
		t.Errorf("Summary = %v, want 1 critical and 1 low", out.Summary)
	}
	if out.Incomplete {
		t.Error("Incomplete = true, want false")
	}
}

func TestAnalyzeReportsSkippedTool(t *testing.T) {
	dir := goRepo(t)
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "broken", err: errFake}},
	})

	out, res := callAnalyze(t, connect(t, s), map[string]any{"path": dir})

	if res.IsError {
		t.Fatalf("a skipped tool must not fail the call: %+v", res.Content)
	}
	if !out.Incomplete {
		t.Error("Incomplete = false, want true when a tool was skipped")
	}
	if len(out.SkippedTools) != 1 || out.SkippedTools[0].Tool != "broken" {
		t.Errorf("SkippedTools = %+v, want one entry for \"broken\"", out.SkippedTools)
	}
}

func TestAnalyzeNoProjectIsToolError(t *testing.T) {
	s := mcpserver.New(nil)

	_, res := callAnalyze(t, connect(t, s), map[string]any{"path": t.TempDir()})

	if !res.IsError {
		t.Fatal("IsError = false, want true when no Go/Rust project is present")
	}
}

func TestToolsAreDiscoverable(t *testing.T) {
	cs := connect(t, mcpserver.New(nil))

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "analyze_codebase" {
			found = true
		}
	}
	if !found {
		t.Errorf("analyze_codebase not advertised; got %+v", tools.Tools)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/mcpserver/...`
Expected: FAIL — `no required module provides package codebase-analyser/internal/mcpserver`.

- [ ] **Step 4: Write `internal/mcpserver/analyze.go`**

```go
package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase-analyser/internal/detect"
	"codebase-analyser/internal/finding"
	"codebase-analyser/internal/orchestrator"
)

// AnalyzeInput is the tool's argument object. Every field is optional
// (`omitempty` keeps it out of the schema's required list), so an agent can
// call analyze_codebase with no arguments at all.
type AnalyzeInput struct {
	Path string `json:"path,omitempty" jsonschema:"path to the repository to analyse; defaults to the server's working directory"`
}

// Finding is the wire shape of one finding. It deliberately omits the
// explanation/fixPattern fields the CLI's JSON report carries: the caller of
// this tool is itself an LLM and does its own explaining.
type Finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Tool     string `json:"tool"`
	RuleID   string `json:"ruleID"`
	Category string `json:"category"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// SkippedTool records a tool that did not run, so partial coverage can never
// be mistaken for a clean pass.
type SkippedTool struct {
	Tool   string `json:"tool"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type AnalyzeOutput struct {
	Total        int            `json:"total"`
	Summary      map[string]int `json:"summary"`
	Categories   map[string]int `json:"categories"`
	Incomplete   bool           `json:"incomplete"`
	SkippedTools []SkippedTool  `json:"skippedTools"`
	Findings     []Finding      `json:"findings"`
}

func (s *Server) analyze(ctx context.Context, _ *mcp.CallToolRequest, in AnalyzeInput) (*mcp.CallToolResult, AnalyzeOutput, error) {
	path := in.Path
	if path == "" {
		path = "."
	}

	projects, skippedPaths, err := detect.Detect(path)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	if len(projects) == 0 {
		return nil, AnalyzeOutput{}, fmt.Errorf("no Go or Rust project found under %s", path)
	}

	findings, skipped := collect(orchestrator.Run(projects, s.adapters))
	for _, p := range skippedPaths {
		skipped = append(skipped, SkippedTool{Reason: "unreadable path during detection: " + p})
	}

	return nil, buildOutput(findings, skipped), nil
}

// collect splits the orchestrator's per-tool results into findings and
// skip records, mirroring what cli.Execute does with the same slice.
func collect(results []orchestrator.ToolResult) ([]finding.Finding, []SkippedTool) {
	var findings []finding.Finding
	var skipped []SkippedTool
	for _, r := range results {
		if r.Skipped {
			skipped = append(skipped, SkippedTool{Tool: r.Tool, Path: r.Path, Reason: r.Error.Error()})
			continue
		}
		findings = append(findings, r.Findings...)
	}
	return findings, skipped
}

func buildOutput(findings []finding.Finding, skipped []SkippedTool) AnalyzeOutput {
	out := AnalyzeOutput{
		Total:        len(findings),
		Summary:      map[string]int{},
		Categories:   map[string]int{},
		Incomplete:   len(skipped) > 0,
		SkippedTools: skipped,
		Findings:     []Finding{},
	}
	if out.SkippedTools == nil {
		out.SkippedTools = []SkippedTool{}
	}
	for _, f := range findings {
		out.Summary[string(f.Severity)]++
		out.Categories[string(f.Category)]++
		out.Findings = append(out.Findings, Finding{
			File: f.File, Line: f.Line, Tool: f.Tool, RuleID: f.RuleID,
			Category: string(f.Category), Severity: string(f.Severity), Message: f.Message,
		})
	}
	return out
}
```

- [ ] **Step 5: Write `internal/mcpserver/server.go`**

```go
// Package mcpserver exposes the analyser pipeline over the Model Context
// Protocol, so any MCP-capable coding agent can run it as a tool. It is a
// second front door onto the same detect -> orchestrator engine the CLI
// drives; it does not reimplement any of it, and it never calls an LLM -
// its caller already is one.
package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/orchestrator"
)

// Version is reported to MCP hosts during initialization. It is a var, not a
// const, so the release build can stamp the real tag into it with -ldflags -X.
var Version = "0.1.0"

type Server struct {
	adapters map[string][]adapter.ToolAdapter
	mcp      *mcp.Server
}

// New builds a server. Passing nil adapters uses the real linters
// (orchestrator.DefaultAdapters); tests pass fakes so nothing shells out.
func New(adapters map[string][]adapter.ToolAdapter) *Server {
	if adapters == nil {
		adapters = orchestrator.DefaultAdapters
	}
	s := &Server{
		adapters: adapters,
		mcp:      mcp.NewServer(&mcp.Implementation{Name: "codebase-analyser", Version: Version}, nil),
	}
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "analyze_codebase",
		Description: "Run static analysis (golangci-lint, gosec, govulncheck, clippy, cargo-audit) over a Go " +
			"and/or Rust repository and return production-safety findings. Blocks until the analysis " +
			"finishes; the first run on a repo may take several minutes because missing tools are " +
			"installed on demand.",
	}, s.analyze)
	return s
}

// MCP exposes the underlying protocol server so main can Run it on a
// transport and tests can connect to it in memory.
func (s *Server) MCP() *mcp.Server { return s.mcp }
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/mcpserver/...`
Expected: PASS (4 tests).

- [ ] **Step 7: Write `cmd/codebase-analyser-mcp/main.go`**

```go
// Command codebase-analyser-mcp serves the analyser over MCP on stdio.
//
// stdout carries the JSON-RPC stream and nothing else - every diagnostic
// goes to stderr, or the host's parser breaks.
package main

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase-analyser/internal/mcpserver"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	log.SetPrefix("codebase-analyser-mcp: ")

	if err := mcpserver.New(nil).MCP().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 8: Verify the whole module builds and passes**

Run: `go build ./... && go test ./...`
Expected: PASS, and `go build ./cmd/codebase-analyser-mcp` produces the binary.

- [ ] **Step 9: Report to the reviewer**

Then emit one single-line commit message and stop; the user commits and pushes before the next task starts. Summarize: files added, test names, and the transitive dependencies the SDK pulled in.

---

### Task 2: Response capping + category/severity filters — tier: `haiku`

The tool's return value becomes tokens in the calling agent's context, so an uncapped response on a large repo is expensive and can crowd out the agent's other context (spec: Tools exposed). Cap the detail, never the counts.

**Files:**
- Modify: `internal/finding/finding.go` (add `SeverityRank`)
- Modify: `internal/finding/finding_test.go` (test it)
- Modify: `internal/mcpserver/analyze.go` (filters + cap)
- Modify: `internal/mcpserver/server.go` (add `maxFindings` field)
- Modify: `internal/mcpserver/analyze_test.go` (add tests)

**Interfaces:**
- Consumes: everything Task 1 produced.
- Produces: `finding.SeverityRank(s finding.Severity) int`; `AnalyzeInput` gains `Category []string` and `Severity string`; `AnalyzeOutput` gains `Shown int`, `Truncated bool`, `Note string`; `mcpserver.New` is unchanged in signature (`maxFindings` defaults to `DefaultMaxFindings` internally).

- [ ] **Step 1: Write the failing test for `finding.SeverityRank`**

Append to `internal/finding/finding_test.go`:

```go
func TestSeverityRankOrdersMostSevereFirst(t *testing.T) {
	if finding.SeverityRank(finding.SeverityCritical) <= finding.SeverityRank(finding.SeverityHigh) {
		t.Error("critical must outrank high")
	}
	if finding.SeverityRank(finding.SeverityLow) <= finding.SeverityRank("bogus") {
		t.Error("a known severity must outrank an unknown one")
	}
}
```

(If `finding_test.go` is an internal test — `package finding` rather than `package finding_test` — drop the `finding.` qualifiers to match the file's existing style.)

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/finding/ -run TestSeverityRank -v`
Expected: FAIL — `undefined: finding.SeverityRank`.

- [ ] **Step 3: Add `SeverityRank`**

In `internal/finding/finding.go`, directly below `MeetsThreshold`:

```go
// SeverityRank exposes the ordering behind MeetsThreshold so callers can sort
// findings most-severe-first. An unknown severity ranks 0, below every known
// one, so it sorts last rather than jumping to the top of a capped list.
func SeverityRank(s Severity) int { return severityRank[s] }
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/finding/ -run TestSeverityRank -v`
Expected: PASS.

- [ ] **Step 5: Write the failing tests for filtering and capping**

Append to `internal/mcpserver/analyze_test.go`:

```go
// manyFindings builds n findings alternating low/critical severity, so a
// capped response is only correct if it sorted by severity first.
func manyFindings(n int) []finding.Finding {
	out := make([]finding.Finding, n)
	for i := range out {
		sev := finding.SeverityLow
		if i%2 == 0 {
			sev = finding.SeverityCritical
		}
		out[i] = finding.Finding{
			File: fmt.Sprintf("f%03d.go", i), Line: i, Tool: "fake",
			RuleID: "R", Category: finding.CategoryCorrectness, Severity: sev,
			Message: "m",
		}
	}
	return out
}

func TestAnalyzeCapsFindingsButNotCounts(t *testing.T) {
	dir := goRepo(t)
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "fake", findings: manyFindings(120)}},
	})

	out, _ := callAnalyze(t, connect(t, s), map[string]any{"path": dir})

	if out.Total != 120 {
		t.Errorf("Total = %d, want 120 (the cap must not change the totals)", out.Total)
	}
	if out.Shown != 50 || len(out.Findings) != 50 {
		t.Errorf("Shown = %d, len(Findings) = %d, want 50 and 50", out.Shown, len(out.Findings))
	}
	if !out.Truncated {
		t.Error("Truncated = false, want true")
	}
	if out.Note == "" {
		t.Error("Note is empty; a truncated response must say so in words")
	}
	if out.Summary["critical"] != 60 || out.Summary["low"] != 60 {
		t.Errorf("Summary = %v, want 60 critical and 60 low across all findings", out.Summary)
	}
	for _, f := range out.Findings {
		if f.Severity != "critical" {
			t.Fatalf("capped list contains %s; the top 50 of 60 criticals must all be critical", f.Severity)
		}
	}
}

func TestAnalyzeFiltersBySeverityAndCategory(t *testing.T) {
	dir := goRepo(t)
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "fake", findings: []finding.Finding{
			{File: "a.go", Tool: "fake", Category: finding.CategorySecurity, Severity: finding.SeverityCritical},
			{File: "b.go", Tool: "fake", Category: finding.CategorySecurity, Severity: finding.SeverityLow},
			{File: "c.go", Tool: "fake", Category: finding.CategoryOperational, Severity: finding.SeverityCritical},
		}}},
	})
	cs := connect(t, s)

	out, _ := callAnalyze(t, cs, map[string]any{"path": dir, "severity": "high"})
	if out.Total != 2 {
		t.Errorf("severity=high: Total = %d, want 2", out.Total)
	}

	out, _ = callAnalyze(t, cs, map[string]any{"path": dir, "category": []string{"security"}})
	if out.Total != 2 {
		t.Errorf("category=security: Total = %d, want 2", out.Total)
	}

	out, _ = callAnalyze(t, cs, map[string]any{"path": dir, "category": []string{"security"}, "severity": "critical"})
	if out.Total != 1 {
		t.Errorf("both filters: Total = %d, want 1", out.Total)
	}
}

func TestAnalyzeRejectsBadFilterValues(t *testing.T) {
	dir := goRepo(t)
	cs := connect(t, mcpserver.New(nil))

	if _, res := callAnalyze(t, cs, map[string]any{"path": dir, "severity": "urgent"}); !res.IsError {
		t.Error("severity=urgent: IsError = false, want true")
	}
	if _, res := callAnalyze(t, cs, map[string]any{"path": dir, "category": []string{"style"}}); !res.IsError {
		t.Error("category=style: IsError = false, want true")
	}
}
```

Add `"fmt"` to the test file's imports.

- [ ] **Step 6: Run them to verify they fail**

Run: `go test ./internal/mcpserver/ -run 'TestAnalyzeCaps|TestAnalyzeFilters|TestAnalyzeRejects' -v`
Expected: FAIL — `out.Shown` undefined, filters ignored.

- [ ] **Step 7: Extend the input and output types**

In `internal/mcpserver/analyze.go`, replace `AnalyzeInput` with:

```go
type AnalyzeInput struct {
	Path     string   `json:"path,omitempty" jsonschema:"path to the repository to analyse; defaults to the server's working directory"`
	Category []string `json:"category,omitempty" jsonschema:"restrict results to these categories: correctness, concurrency, security, operational"`
	Severity string   `json:"severity,omitempty" jsonschema:"only return findings at or above this severity: critical, high, medium, low"`
}
```

and add three fields to `AnalyzeOutput`, after `Total`:

```go
	Shown     int    `json:"shown"`
	Truncated bool   `json:"truncated"`
	Note      string `json:"note,omitempty"`
```

- [ ] **Step 8: Add the filter and cap logic**

In `internal/mcpserver/analyze.go`, add `"sort"` and `"strings"` to the imports, then add:

```go
// DefaultMaxFindings caps how many findings are returned in full detail.
// Counts in Summary/Categories/Total always cover everything.
const DefaultMaxFindings = 50

// filter applies the caller's category/severity narrowing. It runs before the
// cap so the cap always selects from what the caller actually asked for.
func filter(findings []finding.Finding, cats []finding.Category, min finding.Severity) []finding.Finding {
	allowed := map[finding.Category]bool{}
	for _, c := range cats {
		allowed[c] = true
	}
	var out []finding.Finding
	for _, f := range findings {
		if len(allowed) > 0 && !allowed[f.Category] {
			continue
		}
		if min != "" && !finding.MeetsThreshold(f.Severity, min) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// capFindings orders findings most-severe-first and returns at most max of
// them. The tiebreakers (file, line, ruleID) make the truncation
// deterministic: the same repo analysed twice returns the same 50.
func capFindings(fs []finding.Finding, max int) (shown []finding.Finding, truncated bool) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if ra, rb := finding.SeverityRank(a.Severity), finding.SeverityRank(b.Severity); ra != rb {
			return ra > rb
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.RuleID < b.RuleID
	})
	if len(fs) <= max {
		return fs, false
	}
	return fs[:max], true
}

// parseFilters validates the caller's narrowing arguments through the same
// parsers the CLI uses, so the two front doors accept exactly the same values.
func parseFilters(in AnalyzeInput) ([]finding.Category, finding.Severity, error) {
	var cats []finding.Category
	for _, c := range in.Category {
		cat, err := finding.ParseCategory(strings.TrimSpace(c))
		if err != nil {
			return nil, "", err
		}
		cats = append(cats, cat)
	}
	if in.Severity == "" {
		return cats, "", nil
	}
	sev, err := finding.ParseSeverity(strings.TrimSpace(in.Severity))
	if err != nil {
		return nil, "", err
	}
	return cats, sev, nil
}
```

- [ ] **Step 9: Wire filtering and capping into the handler**

In `internal/mcpserver/analyze.go`, replace the body of `analyze` from the `path` assignment through the `return` with:

```go
	path := in.Path
	if path == "" {
		path = "."
	}
	cats, minSev, err := parseFilters(in)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}

	projects, skippedPaths, err := detect.Detect(path)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	if len(projects) == 0 {
		return nil, AnalyzeOutput{}, fmt.Errorf("no Go or Rust project found under %s", path)
	}

	findings, skipped := collect(orchestrator.Run(projects, s.adapters))
	for _, p := range skippedPaths {
		skipped = append(skipped, SkippedTool{Reason: "unreadable path during detection: " + p})
	}
	findings = filter(findings, cats, minSev)

	return nil, buildOutput(findings, skipped, s.maxFindings), nil
```

and replace `buildOutput` with:

```go
func buildOutput(findings []finding.Finding, skipped []SkippedTool, max int) AnalyzeOutput {
	out := AnalyzeOutput{
		Total:        len(findings),
		Summary:      map[string]int{},
		Categories:   map[string]int{},
		Incomplete:   len(skipped) > 0,
		SkippedTools: skipped,
		Findings:     []Finding{},
	}
	if out.SkippedTools == nil {
		out.SkippedTools = []SkippedTool{}
	}
	// Count every finding before capping: the caller must be able to trust
	// the totals even when it only sees the top slice.
	for _, f := range findings {
		out.Summary[string(f.Severity)]++
		out.Categories[string(f.Category)]++
	}

	shown, truncated := capFindings(findings, max)
	out.Shown = len(shown)
	out.Truncated = truncated
	if truncated {
		out.Note = fmt.Sprintf(
			"showing the %d most severe of %d findings; %d not shown. Narrow with the category or severity arguments to see the rest.",
			len(shown), out.Total, out.Total-len(shown))
	}
	for _, f := range shown {
		out.Findings = append(out.Findings, Finding{
			File: f.File, Line: f.Line, Tool: f.Tool, RuleID: f.RuleID,
			Category: string(f.Category), Severity: string(f.Severity), Message: f.Message,
		})
	}
	return out
}
```

- [ ] **Step 10: Add `maxFindings` to the server**

In `internal/mcpserver/server.go`, add the field to `Server`:

```go
type Server struct {
	adapters    map[string][]adapter.ToolAdapter
	maxFindings int
	mcp         *mcp.Server
}
```

and set it in `New`, alongside `adapters`:

```go
	s := &Server{
		adapters:    adapters,
		maxFindings: DefaultMaxFindings,
		mcp:         mcp.NewServer(&mcp.Implementation{Name: "codebase-analyser", Version: Version}, nil),
	}
```

- [ ] **Step 11: Run the full package tests**

Run: `go test ./internal/finding/ ./internal/mcpserver/ -v`
Expected: PASS, including all four Task 1 tests (unchanged behaviour when under the cap and unfiltered).

- [ ] **Step 12: Report to the reviewer**

Then emit one single-line commit message and stop; the user commits and pushes before the next task starts.

---

### Task 3: `push_to_dashboard` — tier: `sonnet`

**Files:**
- Create: `internal/mcpserver/dashboard.go`
- Create: `internal/mcpserver/dashboard_test.go`
- Modify: `internal/mcpserver/server.go` (register the tool, remember the last run)
- Modify: `internal/mcpserver/analyze.go` (record the last run)

**Interfaces:**
- Consumes: Task 1's `Server`, `collect`; `report.RenderJSON(w io.Writer, findings []finding.ExplainedFinding, skipped []report.SkippedTool) error`; `report.SkippedTool{Tool, Path, Reason string}`; `finding.WithoutExplanation([]finding.Finding) []finding.ExplainedFinding`.
- Produces: a `push_to_dashboard` tool taking no arguments; `Server.gitMeta` (a function field tests override).

Read `internal/report/json.go` before starting: the payload's `report` member is byte-for-byte what `report.RenderJSON` writes, so the dashboard ingests the identical document the CLI pushes.

**Contract with the dashboard** (from `docs/superpowers/specs/2026-08-13-dashboard-design.md`):
- `POST {DASHBOARD_URL}/api/repos/{normalized-remote}/runs`
- `Authorization: Bearer {DASHBOARD_TOKEN}`
- Body: `{"branch": "...", "commit": "...", "report": <the CLI's JSON report>}`
- Repo identity is the git remote URL normalized: protocol and `git@` prefix stripped, `:` after the host turned into `/`, `.git` suffix removed. `git@github.com:org/repo.git` and `https://github.com/org/repo` both normalize to `github.com/org/repo`.

- [ ] **Step 1: Write the failing test**

Create `internal/mcpserver/dashboard_test.go`:

```go
package mcpserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/finding"
	"codebase-analyser/internal/mcpserver"
)

func callPush(t *testing.T, cs *mcp.ClientSession) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "push_to_dashboard", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res
}

func TestPushSendsLastRunToDashboard(t *testing.T) {
	type captured struct {
		path, auth string
		body       map[string]json.RawMessage
	}
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]json.RawMessage
		if err := json.Unmarshal(b, &body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		// EscapedPath, not Path: the repo id is percent-encoded into one
		// path segment, and Path would silently decode it back.
		got <- captured{path: r.URL.EscapedPath(), auth: r.Header.Get("Authorization"), body: body}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	t.Setenv("DASHBOARD_URL", srv.URL)
	t.Setenv("DASHBOARD_TOKEN", "secret-token")

	dir := goRepo(t)
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "fake", findings: manyFindings(120)}},
	})
	s.SetGitMeta(func(string) (string, string, string) {
		return "github.com/org/repo", "feature-x", "abc123"
	})
	cs := connect(t, s)

	callAnalyze(t, cs, map[string]any{"path": dir})
	if res := callPush(t, cs); res.IsError {
		t.Fatalf("push failed: %+v", res.Content)
	}

	c := <-got
	if want := "/api/repos/github.com%2Forg%2Frepo/runs"; c.path != want {
		t.Errorf("path = %q, want %q", c.path, want)
	}
	if c.auth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", c.auth, "Bearer secret-token")
	}
	var branch, commit string
	json.Unmarshal(c.body["branch"], &branch)
	json.Unmarshal(c.body["commit"], &commit)
	if branch != "feature-x" || commit != "abc123" {
		t.Errorf("branch/commit = %q/%q, want feature-x/abc123", branch, commit)
	}

	// The dashboard is a persistent record: it gets every finding, not the
	// 50 the agent-facing response is capped to.
	var rep struct {
		Findings []struct{} `json:"findings"`
	}
	if err := json.Unmarshal(c.body["report"], &rep); err != nil {
		t.Fatalf("report member is not the CLI's JSON report: %v", err)
	}
	if len(rep.Findings) != 120 {
		t.Errorf("pushed %d findings, want all 120 (uncapped)", len(rep.Findings))
	}
}

func TestPushWithoutPriorAnalysisIsToolError(t *testing.T) {
	t.Setenv("DASHBOARD_URL", "http://example.invalid")
	t.Setenv("DASHBOARD_TOKEN", "t")

	if res := callPush(t, connect(t, mcpserver.New(nil))); !res.IsError {
		t.Error("IsError = false, want true when no analysis has run yet")
	}
}

func TestPushWithoutConfigIsToolError(t *testing.T) {
	t.Setenv("DASHBOARD_URL", "")
	t.Setenv("DASHBOARD_TOKEN", "")

	dir := goRepo(t)
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "fake", findings: []finding.Finding{{File: "a.go", Severity: finding.SeverityLow, Category: finding.CategoryCorrectness}}}},
	})
	cs := connect(t, s)
	callAnalyze(t, cs, map[string]any{"path": dir})

	if res := callPush(t, cs); !res.IsError {
		t.Error("IsError = false, want true when DASHBOARD_URL/DASHBOARD_TOKEN are unset")
	}
}

func TestPushSurfacesServerRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unknown repo", http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("DASHBOARD_URL", srv.URL)
	t.Setenv("DASHBOARD_TOKEN", "t")

	dir := goRepo(t)
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {fakeAdapter{name: "fake", findings: []finding.Finding{{File: "a.go", Severity: finding.SeverityLow, Category: finding.CategoryCorrectness}}}},
	})
	s.SetGitMeta(func(string) (string, string, string) { return "github.com/org/repo", "main", "deadbeef" })
	cs := connect(t, s)
	callAnalyze(t, cs, map[string]any{"path": dir})

	res := callPush(t, cs)
	if !res.IsError {
		t.Fatal("IsError = false, want true on a 404 from the dashboard")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/mcpserver/ -run TestPush -v`
Expected: FAIL — `s.SetGitMeta` undefined and `unknown tool "push_to_dashboard"`.

- [ ] **Step 3: Write `internal/mcpserver/dashboard.go`**

```go
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase-analyser/internal/finding"
	"codebase-analyser/internal/report"
)

// pushTimeout bounds the dashboard round-trip. The analysis itself may take
// minutes, but an HTTP POST that hangs longer than this is a broken
// dashboard, not a slow one.
const pushTimeout = 30 * time.Second

// maxErrBody caps how much of a failing dashboard response is echoed back
// into the agent's context.
const maxErrBody = 300

// lastRun is the analysis push_to_dashboard sends. It holds the full,
// uncapped finding set: the cap exists to protect the agent's context
// window, and the dashboard is not the agent.
type lastRun struct {
	path     string
	findings []finding.Finding
	skipped  []report.SkippedTool
}

type PushOutput struct {
	Repo     string `json:"repo"`
	Branch   string `json:"branch"`
	Commit   string `json:"commit"`
	Findings int    `json:"findings"`
	Status   string `json:"status"`
}

func (s *Server) push(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, PushOutput, error) {
	s.mu.Lock()
	run := s.last
	s.mu.Unlock()
	if run == nil {
		return nil, PushOutput{}, fmt.Errorf("no analysis to push: call analyze_codebase first")
	}

	base := strings.TrimSpace(os.Getenv("DASHBOARD_URL"))
	token := strings.TrimSpace(os.Getenv("DASHBOARD_TOKEN"))
	if base == "" || token == "" {
		return nil, PushOutput{}, fmt.Errorf("dashboard not configured: set DASHBOARD_URL and DASHBOARD_TOKEN in this MCP server's env block")
	}

	repo, branch, commit := s.gitMeta(run.path)
	if repo == "" {
		return nil, PushOutput{}, fmt.Errorf("cannot identify the repository at %s: it has no git 'origin' remote, and the dashboard keys runs by remote URL", run.path)
	}

	// The report member is byte-for-byte the CLI's JSON report, so both front
	// doors push a document the dashboard parses the same way.
	var reportJSON bytes.Buffer
	if err := report.RenderJSON(&reportJSON, finding.WithoutExplanation(run.findings), run.skipped); err != nil {
		return nil, PushOutput{}, fmt.Errorf("render report: %w", err)
	}
	body, err := json.Marshal(map[string]any{
		"branch": branch,
		"commit": commit,
		"report": json.RawMessage(reportJSON.Bytes()),
	})
	if err != nil {
		return nil, PushOutput{}, fmt.Errorf("encode payload: %w", err)
	}

	endpoint := strings.TrimSuffix(base, "/") + "/api/repos/" + url.PathEscape(repo) + "/runs"
	reqCtx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, PushOutput{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, PushOutput{}, fmt.Errorf("push to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
		// Never echo the request back on failure - it would put the bearer
		// token into the agent's context.
		return nil, PushOutput{}, fmt.Errorf("dashboard rejected the run: %s: %s", resp.Status, bytes.TrimSpace(snippet))
	}

	return nil, PushOutput{
		Repo: repo, Branch: branch, Commit: commit,
		Findings: len(run.findings), Status: resp.Status,
	}, nil
}

// gitMeta reads repo identity from the working tree with read-only git
// commands. A missing git binary or a path that is not a repo yields empty
// strings rather than an error; the caller decides which of the three it
// actually needs.
func gitMeta(path string) (remote, branch, commit string) {
	return normalizeRemote(gitOut(path, "remote", "get-url", "origin")),
		gitOut(path, "rev-parse", "--abbrev-ref", "HEAD"),
		gitOut(path, "rev-parse", "HEAD")
}

func gitOut(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// normalizeRemote reduces a git remote URL to the host/path form the
// dashboard keys repos by, so the same repo cloned over SSH and over HTTPS
// lands on one record.
func normalizeRemote(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	for _, prefix := range []string{"https://", "http://", "ssh://", "git://"} {
		u = strings.TrimPrefix(u, prefix)
	}
	if at := strings.Index(u, "@"); at >= 0 {
		u = u[at+1:]
	}
	u = strings.Replace(u, ":", "/", 1) // git@github.com:org/repo -> github.com/org/repo
	u = strings.TrimSuffix(u, "/")
	return strings.TrimSuffix(u, ".git")
}
```

- [ ] **Step 4: Record the last run in the analyze handler**

In `internal/mcpserver/analyze.go`, immediately after the `findings = filter(...)` line in `analyze`, add:

```go
	// Remember the unfiltered, uncapped run for push_to_dashboard. It is
	// recorded after filtering deliberately: the agent asked for this view,
	// and pushing a different set than it just saw would be surprising.
	s.mu.Lock()
	s.last = &lastRun{path: path, findings: findings, skipped: toReportSkipped(skipped)}
	s.mu.Unlock()
```

and add the converter at the bottom of the file:

```go
// toReportSkipped adapts the MCP wire type to the report package's own
// SkippedTool so RenderJSON can be reused verbatim.
func toReportSkipped(skipped []SkippedTool) []report.SkippedTool {
	out := make([]report.SkippedTool, 0, len(skipped))
	for _, s := range skipped {
		out = append(out, report.SkippedTool{Tool: s.Tool, Path: s.Path, Reason: s.Reason})
	}
	return out
}
```

Add `"codebase-analyser/internal/report"` to the imports.

- [ ] **Step 5: Register the tool and the state on the server**

In `internal/mcpserver/server.go`, add `"sync"` to the imports and update the struct and constructor:

```go
type Server struct {
	adapters    map[string][]adapter.ToolAdapter
	maxFindings int
	mcp         *mcp.Server

	// gitMeta is a field, not a direct call, so tests can supply repo
	// identity without creating a real git repository. Same seam the
	// explain package uses for os.Getenv.
	gitMeta func(path string) (remote, branch, commit string)

	mu   sync.Mutex
	last *lastRun
}
```

In `New`, set `gitMeta: gitMeta,` in the struct literal, and register the second tool after `analyze_codebase`:

```go
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "push_to_dashboard",
		Description: "Push the most recent analyze_codebase result to the configured dashboard. " +
			"Requires DASHBOARD_URL and DASHBOARD_TOKEN in this server's environment; the token is " +
			"never returned to the caller. Takes no arguments.",
	}, s.push)
```

Then add the test seam at the bottom of the file:

```go
// SetGitMeta overrides how repo identity is read. Tests use it to avoid
// depending on a real git checkout.
func (s *Server) SetGitMeta(f func(path string) (remote, branch, commit string)) { s.gitMeta = f }
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/mcpserver/ -v`
Expected: PASS — all Task 1, Task 2 and Task 3 tests.

- [ ] **Step 7: Verify the token never reaches the caller**

Run: `go test ./internal/mcpserver/ -run TestPushSurfacesServerRejection -v`
Expected: PASS. Read the error path in `push` once more and confirm no code path formats `token`, `req.Header`, or `body` into a returned error.

- [ ] **Step 8: Report to the reviewer**

Then emit one single-line commit message and stop; the user commits and pushes before the next task starts.

---

### Task 4: stdio conformance smoke test + host configuration docs — tier: `sonnet`

**Files:**
- Create: `mcp_e2e_test.go` (repo root, `package e2e` — matches the existing `e2e_test.go`)
- Create: `docs/mcp-server.md`

**Interfaces:**
- Consumes: the built `cmd/codebase-analyser-mcp` binary; `mcp.CommandTransport`.
- Produces: nothing other tasks depend on.

This is the only test that exercises the real thing an MCP host does: spawn the binary and speak JSON-RPC over its stdin/stdout. It deliberately points `analyze_codebase` at an empty directory so it never invokes a real linter — the subject under test is the protocol path, not the pipeline (which `internal/mcpserver` and `e2e_test.go` already cover).

- [ ] **Step 1: Write the failing test**

Create `mcp_e2e_test.go`:

```go
package e2e

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// buildMCPServer compiles the server binary into a temp dir and returns its
// path.
func buildMCPServer(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "codebase-analyser-mcp")
	out, err := exec.Command("go", "build", "-o", bin, "codebase-analyser/cmd/codebase-analyser-mcp").CombinedOutput()
	if err != nil {
		t.Fatalf("build server: %v\n%s", err, out)
	}
	return bin
}

// TestMCPStdioConformance spawns the real binary and drives it over stdio,
// the exact path an MCP host takes.
func TestMCPStdioConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and spawns a binary")
	}
	ctx := t.Context()
	bin := buildMCPServer(t)

	client := mcp.NewClient(&mcp.Implementation{Name: "conformance", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(bin)}, nil)
	if err != nil {
		t.Fatalf("connect over stdio: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"analyze_codebase", "push_to_dashboard"} {
		if !names[want] {
			t.Errorf("tool %q not advertised; got %v", want, names)
		}
	}

	// An empty directory has no go.mod/Cargo.toml, so this exercises the
	// full request/response path without running a single linter.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "analyze_codebase",
		Arguments: map[string]any{"path": t.TempDir()},
	})
	if err != nil {
		t.Fatalf("CallTool(analyze_codebase): %v", err)
	}
	if !res.IsError {
		t.Error("analyze_codebase on an empty dir: IsError = false, want true")
	}
	if got := textOf(res); !strings.Contains(got, "no Go or Rust project") {
		t.Errorf("error text = %q, want it to name the missing project", got)
	}

	// DASHBOARD_URL/TOKEN are unset in this process, so the push must fail
	// cleanly rather than hang or crash the server.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "push_to_dashboard", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool(push_to_dashboard): %v", err)
	}
	if !res.IsError {
		t.Error("push_to_dashboard with no prior analysis: IsError = false, want true")
	}

	// The session is still usable after two tool errors: an error result is
	// a normal response, not a broken connection.
	if _, err := session.ListTools(ctx, nil); err != nil {
		t.Errorf("session unusable after tool errors: %v", err)
	}
}

func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
```

- [ ] **Step 2: Run it**

Run: `go test . -run TestMCPStdioConformance -v`
Expected: PASS. If it fails at `connect over stdio`, the binary is writing something to stdout — check that every diagnostic in `main.go` and anything it calls goes to stderr.

- [ ] **Step 3: Write `docs/mcp-server.md`**

```markdown
# Codebase Analyser MCP Server

`codebase-analyser-mcp` exposes the analyser to any MCP-compatible coding
agent as two tools:

| Tool | Arguments | Returns |
|---|---|---|
| `analyze_codebase` | `path` (optional, defaults to the server's working directory), `category` (optional list: `correctness`, `concurrency`, `security`, `operational`), `severity` (optional: `critical`, `high`, `medium`, `low`) | Findings, per-severity and per-category totals, and which tools were skipped |
| `push_to_dashboard` | none | Pushes the most recent analysis to the configured dashboard |

Findings are returned raw, with no LLM explanation: the caller is already a
model and does its own explaining.

## Response size

A response carries full detail for the 50 most severe findings and accurate
totals for all of them, with a `note` saying how many were withheld. The
return value lands directly in the calling agent's context, so an uncapped
response on a large repository would be expensive. Narrow with `category`
and `severity` to see the rest.

## Latency

Calls block until the analysis finishes. The first run against a repository
can take several minutes: missing linters are installed on demand. Each tool
is capped at 5 minutes.

## Configuration

### Claude Code — `.mcp.json` in the project, or `~/.claude.json`

```json
{
  "mcpServers": {
    "codebase-analyser": {
      "command": "codebase-analyser-mcp",
      "env": {
        "DASHBOARD_URL": "https://dashboard.example.com",
        "DASHBOARD_TOKEN": "your-repo-ingest-token"
      }
    }
  }
}
```

### Codex CLI — `~/.codex/config.toml`

```toml
[mcp_servers.codebase-analyser]
command = "codebase-analyser-mcp"

[mcp_servers.codebase-analyser.env]
DASHBOARD_URL = "https://dashboard.example.com"
DASHBOARD_TOKEN = "your-repo-ingest-token"
```

### Gemini CLI — `~/.gemini/settings.json`

```json
{
  "mcpServers": {
    "codebase-analyser": {
      "command": "codebase-analyser-mcp",
      "env": {
        "DASHBOARD_URL": "https://dashboard.example.com",
        "DASHBOARD_TOKEN": "your-repo-ingest-token"
      }
    }
  }
}
```

The dashboard variables are optional; omit them if you only want
`analyze_codebase`. `DASHBOARD_TOKEN` is read from the server's environment
and never returned to the model.

## Building

```bash
go build -o codebase-analyser-mcp ./cmd/codebase-analyser-mcp
```
```

- [ ] **Step 4: Verify the whole module is green**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Report to the reviewer — Stage 1 is complete**

Then emit one single-line commit message and stop; the user commits and pushes before the next task starts. State plainly that a host can now be pointed at the binary, and that Stages 2–4 remain.

---

## Stage 2 — Incremental analysis cache

> **Read this before starting Stage 2.** golangci-lint and clippy already
> cache their analysis per package/crate (Go build cache, cargo target dir),
> so a warm re-run may already be fast enough to make this stage
> unnecessary. Task 5 Step 1 measures that. If the measured warm re-run of
> `analyze_codebase` on a real repository is under ~5 seconds, stop and take
> the measurement to the reviewer instead of building the cache — the
> spec's goal is a fast fix-and-recheck loop, not a cache for its own sake.

### Task 5: Fingerprints and the on-disk cache store — tier: `sonnet`

**Files:**
- Create: `internal/cache/cache.go`
- Create: `internal/cache/cache_test.go`
- Modify: `internal/adapter/adapter.go` (export `ResolveCommand`)

**Interfaces:**
- Consumes: `finding.Finding`.
- Produces:
  - `adapter.ResolveCommand(name string) (path string, ok bool)` — exported wrapper over the existing unexported `resolveCommand`.
  - `cache.Root() (string, error)` — the tool's cache root, overridable with `CODEBASE_ANALYSER_CACHE` (Task 9's toolchain downloader shares it)
  - `cache.Fingerprint(dir string, exts []string) (string, error)`
  - `cache.ToolStamp(toolName string) string`
  - `cache.Open(repoPath string) (*cache.Store, error)`
  - `(*Store).Get(tool, stamp, unit, fingerprint string) ([]finding.Finding, bool)`
  - `(*Store).Put(tool, stamp, unit, fingerprint string, fs []finding.Finding) error`
  - `(*Store).Save() error`

- [ ] **Step 1: Measure the baseline before building anything**

Run, from the repo root, twice in a row:

```bash
go build -o /tmp/ca-mcp ./cmd/codebase-analyser-mcp
time go test . -run TestMCPStdioConformance   # warms nothing; just proves the binary works
```

Then measure a real analysis of this repository itself, twice, and record both wall times:

```bash
time go run ./cmd/analyser run . --no-llm --format json > /dev/null
time go run ./cmd/analyser run . --no-llm --format json > /dev/null
```

Write both numbers into your report to the reviewer. **If the second run is under ~5 seconds, stop here** and hand the measurement back — Stage 2 is not worth its complexity on this codebase.

- [ ] **Step 2: Export `ResolveCommand`**

In `internal/adapter/adapter.go`, below the existing `resolveCommand`:

```go
// ResolveCommand exposes tool-binary resolution (PATH, then the Go bin dir)
// to other packages. The cache keys entries by the resolved binary's
// identity, so replacing a linter invalidates its cached findings.
func ResolveCommand(name string) (path string, ok bool) { return resolveCommand(name) }
```

- [ ] **Step 3: Write the failing test**

Create `internal/cache/cache_test.go`:

```go
package cache_test

import (
	"os"
	"path/filepath"
	"testing"

	"codebase-analyser/internal/cache"
	"codebase-analyser/internal/finding"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFingerprintChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.go", "package a\n")

	first, err := cache.Fingerprint(dir, []string{".go"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := cache.Fingerprint(dir, []string{".go"})
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Error("fingerprint is not stable across calls on unchanged files")
	}

	write(t, dir, "a.go", "package a\n\nvar x = 1\n")
	changed, err := cache.Fingerprint(dir, []string{".go"})
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Error("fingerprint did not change after editing a file")
	}
}

func TestFingerprintIgnoresIrrelevantExtensions(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.go", "package a\n")
	before, _ := cache.Fingerprint(dir, []string{".go"})

	write(t, dir, "README.md", "docs")
	after, _ := cache.Fingerprint(dir, []string{".go"})

	if before != after {
		t.Error("a non-.go file changed the .go fingerprint")
	}
}

func TestFingerprintDoesNotDescendIntoSubdirectories(t *testing.T) {
	// Invalidation is per package/crate: a change in a child package must
	// not invalidate its parent's entry, or the cache degrades to all-or-nothing.
	dir := t.TempDir()
	write(t, dir, "a.go", "package a\n")
	sub := filepath.Join(dir, "child")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	before, _ := cache.Fingerprint(dir, []string{".go"})

	write(t, sub, "b.go", "package child\n")
	after, _ := cache.Fingerprint(dir, []string{".go"})

	if before != after {
		t.Error("a file in a child directory changed the parent's fingerprint")
	}
}

func TestStoreRoundTripsAcrossReopen(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	repo := t.TempDir()
	want := []finding.Finding{{File: "a.go", Line: 2, Tool: "fake", RuleID: "R1",
		Category: finding.CategorySecurity, Severity: finding.SeverityHigh, Message: "m"}}

	s, err := cache.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put("fake", "stamp-1", "./pkg", "fp-1", want); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	reopened, err := cache.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Get("fake", "stamp-1", "./pkg", "fp-1")
	if !ok {
		t.Fatal("entry not found after reopening the store")
	}
	if len(got) != 1 || got[0].RuleID != "R1" || got[0].Severity != finding.SeverityHigh {
		t.Errorf("round-tripped findings = %+v, want %+v", got, want)
	}
}

func TestStoreMissesOnChangedFingerprintOrStamp(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	s, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Put("fake", "stamp-1", "./pkg", "fp-1", []finding.Finding{{File: "a.go"}})

	if _, ok := s.Get("fake", "stamp-1", "./pkg", "fp-2"); ok {
		t.Error("hit on a changed fingerprint; the source changed and the entry is stale")
	}
	if _, ok := s.Get("fake", "stamp-2", "./pkg", "fp-1"); ok {
		t.Error("hit on a changed tool stamp; the linter was replaced and the entry is stale")
	}
}

func TestSeparateReposDoNotShareEntries(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	a, _ := cache.Open(t.TempDir())
	b, _ := cache.Open(t.TempDir())
	a.Put("fake", "s", "./pkg", "fp", []finding.Finding{{File: "a.go"}})
	a.Save()

	if _, ok := b.Get("fake", "s", "./pkg", "fp"); ok {
		t.Error("a second repo saw the first repo's cached findings")
	}
}
```

- [ ] **Step 4: Run it to verify it fails**

Run: `go test ./internal/cache/...`
Expected: FAIL — package does not exist.

- [ ] **Step 5: Write `internal/cache/cache.go`**

```go
// Package cache stores per-package analysis results between runs so an
// agent's fix-and-recheck loop only re-lints what actually changed.
//
// Invalidation is per package/crate, never per file: these tools reason at
// the package level, so a change in one file can alter a diagnostic reported
// against a different file in the same package. File-level invalidation
// would silently serve stale findings.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/finding"
)

// Fingerprint hashes the names, sizes and contents of the files in dir with
// one of the given extensions. It does not descend into subdirectories: each
// package/crate directory is fingerprinted on its own so a change in a child
// package invalidates only that child.
func Fingerprint(dir string, exts []string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	wanted := map[string]bool{}
	for _, e := range exts {
		wanted[e] = true
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !wanted[filepath.Ext(e.Name())] {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names) // ReadDir order is not guaranteed across platforms

	h := sha256.New()
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ToolStamp identifies the binary that will run, so replacing or upgrading a
// linter invalidates everything it cached. The binary's path, size and
// modification time stand in for a parsed --version string: same guarantee,
// no per-tool version-flag handling. A tool that cannot be resolved stamps
// as "missing", which never matches a stamp recorded from a real binary.
func ToolStamp(toolName string) string {
	path, ok := adapter.ResolveCommand(toolName)
	if !ok {
		return "missing"
	}
	info, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	return path + "|" + strconv.FormatInt(info.Size(), 10) + "|" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

type entry struct {
	Stamp       string            `json:"stamp"`
	Fingerprint string            `json:"fingerprint"`
	Findings    []finding.Finding `json:"findings"`
}

// Store is one repository's cache. Entries are held in memory during a run
// and flushed once by Save, so a run with many packages does not rewrite the
// file once per package.
type Store struct {
	file string

	mu      sync.Mutex
	entries map[string]entry // "tool\x00unit" -> entry
	dirty   bool
}

// Open loads (or starts) the cache for repoPath. A corrupt or unreadable
// cache file is treated as empty rather than fatal: a stale cache must never
// be able to stop an analysis from running.
func Open(repoPath string) (*Store, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(abs))
	dir, err := cacheDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	s := &Store{
		file:    filepath.Join(dir, hex.EncodeToString(sum[:16])+".json"),
		entries: map[string]entry{},
	}
	if b, err := os.ReadFile(s.file); err == nil {
		_ = json.Unmarshal(b, &s.entries)
	}
	return s, nil
}

// Root is where everything this tool caches lives: analysis results here,
// downloaded toolchains alongside. CODEBASE_ANALYSER_CACHE overrides it -
// os.UserCacheDir honours XDG_CACHE_HOME on Linux but nothing equivalent on
// macOS or Windows, so tests need a seam that works everywhere.
func Root() (string, error) {
	if override := os.Getenv("CODEBASE_ANALYSER_CACHE"); override != "" {
		return override, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "codebase-analyser"), nil
}

func cacheDir() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "analysis"), nil
}

func key(tool, unit string) string { return tool + "\x00" + unit }

// Get returns the cached findings for one tool against one package/crate,
// but only if both the tool binary and the package's contents are unchanged.
func (s *Store) Get(tool, stamp, unit, fingerprint string) ([]finding.Finding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key(tool, unit)]
	if !ok || e.Stamp != stamp || e.Fingerprint != fingerprint {
		return nil, false
	}
	return e.Findings, true
}

func (s *Store) Put(tool, stamp, unit, fingerprint string, fs []finding.Finding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key(tool, unit)] = entry{Stamp: stamp, Fingerprint: fingerprint, Findings: fs}
	s.dirty = true
	return nil
}

// Save flushes the cache. It writes to a temp file and renames, so an
// interrupted run leaves the previous cache intact rather than a half-written
// one.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	b, err := json.Marshal(s.entries)
	if err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.file); err != nil {
		return err
	}
	s.dirty = false
	return nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/cache/... ./internal/adapter/... -v`
Expected: PASS.

- [ ] **Step 7: Report to the reviewer**

Include the two baseline timings from Step 1. Then emit one single-line commit message and stop; the user commits and pushes before the next task starts.

---

### Task 6: Package/crate-scoped re-runs — tier: `sonnet`

**Files:**
- Create: `internal/cache/packages.go`
- Create: `internal/cache/packages_test.go`
- Modify: `internal/adapter/adapter.go` (add the `Targeted` optional interface)
- Modify: `internal/adapter/golangci.go`, `internal/adapter/gosec.go`, `internal/adapter/clippy.go` (implement it)
- Modify: `internal/mcpserver/analyze.go` (consult the cache)
- Modify: `internal/mcpserver/analyze_test.go` (add the cache-hit test)

**Interfaces:**
- Consumes: everything Task 5 produced; the existing `adapter.ToolAdapter`.
- Produces: `adapter.Targeted` interface; `orchestrator.Cache` interface and `orchestrator.RunWithCache`; `cache.Units(project detect.Project) ([]cache.Unit, error)`; `cache.Unit{Dir, Target string; Exts []string}`.

Read `internal/adapter/golangci.go`, `gosec.go` and `clippy.go` first — you need each tool's existing argument list before adding a targeted variant.

`govulncheck` and `cargo-audit` are whole-module scanners (they answer "does this dependency set contain a known vulnerability?"), so they gain nothing from package targeting and deliberately do **not** implement `Targeted`. Adapters that do not implement it always run in full and are never cached.

- [ ] **Step 1: Write the failing test for unit enumeration**

Create `internal/cache/packages_test.go`:

```go
package cache_test

import (
	"os"
	"path/filepath"
	"testing"

	"codebase-analyser/internal/cache"
	"codebase-analyser/internal/detect"
)

func TestUnitsEnumeratesGoPackageDirectories(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module fixture\n\ngo 1.26\n")
	write(t, root, "main.go", "package main\n")
	sub := filepath.Join(root, "internal", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, sub, "p.go", "package pkg\n")
	// A directory with no Go files is not a package.
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	units, err := cache.Units(detect.Project{Path: root, Language: "go"})
	if err != nil {
		t.Fatal(err)
	}

	targets := map[string]bool{}
	for _, u := range units {
		targets[u.Target] = true
	}
	if !targets["./"] {
		t.Errorf("root package missing; got targets %v", targets)
	}
	if !targets["./internal/pkg"] {
		t.Errorf("nested package missing; got targets %v", targets)
	}
	if targets["./docs"] {
		t.Error("a directory with no Go files was treated as a package")
	}
}

func TestUnitsTreatsARustCrateAsOneUnit(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"fixture\"\nversion = \"0.1.0\"\n")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "src"), "main.rs", "fn main() {}\n")

	units, err := cache.Units(detect.Project{Path: root, Language: "rust"})
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 1 {
		t.Fatalf("len(units) = %d, want 1 (one crate, one unit)", len(units))
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/cache/ -run TestUnits -v`
Expected: FAIL — `undefined: cache.Units`.

- [ ] **Step 3: Write `internal/cache/packages.go`**

```go
package cache

import (
	"io/fs"
	"os"
	"path/filepath"

	"codebase-analyser/internal/detect"
)

// Unit is one independently-cacheable piece of a project: a Go package
// directory, or a whole Rust crate. Dir is the absolute path fingerprinted;
// Target is what gets passed to the tool to restrict its run.
type Unit struct {
	Dir    string
	Target string
	Exts   []string
}

// goExts and rustExts are the file kinds whose contents can change a tool's
// diagnostics for a unit.
var (
	goExts   = []string{".go"}
	rustExts = []string{".rs", ".toml", ".lock"}
)

// Units enumerates a project's cacheable units. For Go that is every
// directory containing at least one .go file; for Rust it is the crate root,
// because clippy type-checks a crate as a whole and cannot report on less
// than one.
func Units(project detect.Project) ([]Unit, error) {
	if project.Language == "rust" {
		return []Unit{{Dir: project.Path, Target: ".", Exts: rustExts}}, nil
	}

	var units []Unit
	err := filepath.WalkDir(project.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Mirror detect.Detect: an unreadable subtree is skipped, not fatal.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case ".git", "node_modules", "vendor", "target", "testdata":
			return filepath.SkipDir
		}
		if !hasExt(path, goExts) {
			return nil
		}
		rel, err := filepath.Rel(project.Path, path)
		if err != nil {
			return nil
		}
		target := "./" + filepath.ToSlash(rel)
		if rel == "." {
			target = "./"
		}
		units = append(units, Unit{Dir: path, Target: target, Exts: goExts})
		return nil
	})
	return units, err
}

func hasExt(dir string, exts []string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		for _, ext := range exts {
			if filepath.Ext(e.Name()) == ext {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/cache/ -run TestUnits -v`
Expected: PASS.

- [ ] **Step 5: Add the `Targeted` optional interface**

In `internal/adapter/adapter.go`, below the `ToolAdapter` interface:

```go
// Targeted is implemented by adapters that can restrict a run to a subset of
// a project's packages/crates, which is what makes incremental caching
// possible. It is optional on purpose: govulncheck and cargo-audit scan a
// whole dependency set and have nothing smaller to be restricted to, so they
// always run in full.
type Targeted interface {
	ToolAdapter
	RunTargets(path string, targets []string) ([]finding.Finding, error)
}
```

- [ ] **Step 5b: Verify against REAL tool output that positional targets work**

Do this BEFORE writing `RunTargets`. This codebase has been bitten twice by hand-authored fixtures that did not match what the tool actually emits: `golangci.go` passed `--out-format json` (removed in golangci-lint v2) and so produced zero findings on every current install while its fixture passed happily, and `cargoaudit.go` parsed a `advisory.informational` field that is always null in real output, silently dropping a whole category of advisory. Both had green tests.

Task 6's whole premise is that these tools accept a positional package path and restrict their run to it. Prove it before building on it. Run each command against this repository and read the actual output:

```bash
# Does golangci-lint accept a single package dir positionally, and still emit the same JSON shape?
golangci-lint run --output.json.path stdout --output.text.path stderr --show-stats=false \
  --default none --enable govet,errcheck,staticcheck,contextcheck,bodyclose,noctx ./internal/detect | head -40

# Does gosec?
gosec -fmt=json ./internal/detect | head -40
```

Confirm for each: (a) exit is not a usage error, (b) stdout is the same JSON structure the existing parser reads, (c) the findings are restricted to that package rather than the whole module. If a tool rejects a bare directory, try `./internal/detect/...` — a single-package pattern with the recursive suffix — and if THAT is what works, change `cache.Units` in Step 3 to emit `<target>/...` form instead, and say so in your report.

Real captures from previous verification live in `.superpowers/sdd/2026-08-13-codebase-analyser-cli/real-tool-output/` — read-only, but useful for comparing shapes. Do not trust `internal/adapter/testdata/*_sample.json` as evidence of what a tool emits; those are fixtures, and fixtures are what failed twice.

If a tool is not installed locally, say so in your report and mark that adapter's targeting as unverified rather than assuming it works.

- [ ] **Step 6: Implement `RunTargets` on the three adapters that can target**

In each case `Run` becomes a one-line delegation, so the targeted and untargeted paths can never drift apart in flag handling or parsing. Every existing flag is preserved exactly; the parse half of each method is unchanged.

`internal/adapter/golangci.go` — replace `Run` (keep the long comment block above it as-is):

```go
func (g GolangciLint) Run(path string) ([]finding.Finding, error) {
	return g.RunTargets(path, nil)
}

// RunTargets restricts the run to the given package patterns; empty targets
// means the whole module, which is what Run has always done.
func (GolangciLint) RunTargets(path string, targets []string) ([]finding.Finding, error) {
	if len(targets) == 0 {
		targets = []string{"./..."}
	}
	args := append([]string{"run",
		"--output.json.path", "stdout",
		"--output.text.path", "stderr",
		"--show-stats=false",
		"--default", "none",
		"--enable", "govet,errcheck,staticcheck,contextcheck,bodyclose,noctx",
	}, targets...)
	out, err := runCommand(path, "golangci-lint", args...)
	if err != nil {
		return nil, fmt.Errorf("golangci-lint: %w", err)
	}
	var parsed golangciOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("golangci-lint: parsing output: %w", err)
	}
	return golangciFindings(parsed), nil
}
```

`internal/adapter/gosec.go` — replace `Run`:

```go
func (g Gosec) Run(path string) ([]finding.Finding, error) {
	return g.RunTargets(path, nil)
}

func (Gosec) RunTargets(path string, targets []string) ([]finding.Finding, error) {
	if len(targets) == 0 {
		targets = []string{"./..."}
	}
	args := append([]string{"-fmt=json"}, targets...)
	out, err := runCommand(path, "gosec", args...)
	if err != nil {
		return nil, fmt.Errorf("gosec: %w", err)
	}
	var parsed gosecOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("gosec: parsing output: %w", err)
	}
	return gosecFindings(parsed), nil
}
```

`internal/adapter/clippy.go` — replace `Run`:

```go
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
```

- [ ] **Step 6b: Verify the adapters still behave identically**

Run: `go test ./internal/adapter/... -v`
Expected: PASS with no test edits. If a test needed changing, the refactor changed behaviour — undo and redo it as a pure delegation.

Also confirm the interface is satisfied by adding this to `internal/adapter/adapter_test.go` (or a new `targeted_test.go` in the same package as the existing adapter tests):

```go
func TestOnlyPackageScopedAdaptersAreTargeted(t *testing.T) {
	targeted := []adapter.ToolAdapter{adapter.GolangciLint{}, adapter.Gosec{}, adapter.Clippy{}}
	for _, a := range targeted {
		if _, ok := a.(adapter.Targeted); !ok {
			t.Errorf("%s does not implement Targeted", a.Name())
		}
	}
	// Whole-dependency-set scanners have nothing smaller to be restricted to.
	for _, a := range []adapter.ToolAdapter{adapter.Govulncheck{}, adapter.CargoAudit{}} {
		if _, ok := a.(adapter.Targeted); ok {
			t.Errorf("%s implements Targeted but scans a whole dependency set", a.Name())
		}
	}
}
```

- [ ] **Step 7: Write the failing cache-hit test**

Append to `internal/mcpserver/analyze_test.go`:

```go
// countingAdapter records which targets it was asked to run, so the test can
// prove the second analysis skipped the unchanged package.
type countingAdapter struct {
	fakeAdapter
	runs *[][]string
}

func (c countingAdapter) RunTargets(path string, targets []string) ([]finding.Finding, error) {
	*c.runs = append(*c.runs, targets)
	return c.findings, c.err
}

func TestAnalyzeSkipsUnchangedPackagesOnSecondCall(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	dir := goRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var runs [][]string
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {countingAdapter{
			fakeAdapter: fakeAdapter{name: "fake", findings: []finding.Finding{
				{File: "a.go", Line: 1, Tool: "fake", RuleID: "R1",
					Category: finding.CategoryCorrectness, Severity: finding.SeverityHigh, Message: "m"},
			}},
			runs: &runs,
		}},
	})
	cs := connect(t, s)

	first, _ := callAnalyze(t, cs, map[string]any{"path": dir})
	if first.Total != 1 {
		t.Fatalf("first call Total = %d, want 1", first.Total)
	}
	runsAfterFirst := len(runs)
	if runsAfterFirst == 0 {
		t.Fatal("the first call never invoked the adapter")
	}

	second, _ := callAnalyze(t, cs, map[string]any{"path": dir})
	if second.Total != 1 {
		t.Errorf("second call Total = %d, want 1 (cached findings must still be reported)", second.Total)
	}
	if len(runs) != runsAfterFirst {
		t.Errorf("adapter ran again on an unchanged package: %v", runs[runsAfterFirst:])
	}

	// Editing a file must bring its package back.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package fixture\n\nvar x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _ = callAnalyze(t, cs, map[string]any{"path": dir}); len(runs) == runsAfterFirst {
		t.Error("adapter did not re-run after the package's source changed")
	}
}
```

Add `"os"` and `"path/filepath"` to the test file's imports if they are not already there.

- [ ] **Step 8: Run it to verify it fails**

Run: `go test ./internal/mcpserver/ -run TestAnalyzeSkipsUnchanged -v`
Expected: FAIL — the adapter runs both times.

- [ ] **Step 9: Teach the orchestrator to consult a cache**

In `internal/orchestrator/orchestrator.go`, add the interface above `Run`:

```go
// Cache lets Run serve part of a tool's work from a previous run. It is an
// interface declared here, rather than an import of internal/cache, so the
// orchestrator stays independent of where results are stored.
type Cache interface {
	// Lookup splits one (tool, project) pair into what is already known and
	// what still has to run. ok=false means "do not cache this pair" and the
	// tool runs in full.
	Lookup(tool string, p detect.Project) (stale []string, cached []finding.Finding, ok bool)

	// Store records what a real run produced. ran is the target list that was
	// actually run, so units that produced no findings are still recorded as
	// clean rather than being re-run forever.
	Store(tool string, p detect.Project, ran []string, produced []finding.Finding)
}
```

Change `Run` into a wrapper and add the cached variant:

```go
// Run executes every adapter for every detected project concurrently, with no
// caching. See RunWithCache.
func Run(projects []detect.Project, adaptersByLang map[string][]adapter.ToolAdapter) []ToolResult {
	return RunWithCache(projects, adaptersByLang, nil)
}

// RunWithCache is Run with an optional cache. An adapter that does not
// implement adapter.Targeted cannot be restricted to a subset of a project,
// so it always runs in full and never consults c.
func RunWithCache(projects []detect.Project, adaptersByLang map[string][]adapter.ToolAdapter, c Cache) []ToolResult {
	var wg sync.WaitGroup
	resultsCh := make(chan ToolResult)
	inst := newInstaller()

	for _, p := range projects {
		for _, a := range adaptersByLang[p.Language] {
			wg.Add(1)
			go func(a adapter.ToolAdapter, p detect.Project) {
				defer wg.Done()
				resultsCh <- runOne(a, p, inst, c)
			}(a, p)
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
```

Replace `runOne` with the version below. It keeps the existing panic recovery and installer memoization verbatim; the only new code is the cache branch:

```go
// runOne runs a single adapter against a single project. It never lets the
// calling goroutine crash: a panic inside CheckInstalled/Install/Run is
// recovered and reported as a skipped result carrying the panic value and
// stack trace, so one broken adapter can never take down the rest of the run.
func runOne(a adapter.ToolAdapter, p detect.Project, inst *installer, c Cache) (result ToolResult) {
	defer func() {
		if r := recover(); r != nil {
			// Path must be set here too. It was missing on this branch once
			// before and a panicking adapter rendered as "skipped gosec ():
			// panic..." with an empty path in the JSON report; a review
			// caught it. Do not drop it while moving the parameter list.
			result = ToolResult{Tool: a.Name(), Path: p.Path, Skipped: true, Error: fmt.Errorf("panic: %v\n%s", r, debug.Stack())}
		}
	}()

	if err := inst.ensure(a); err != nil {
		return ToolResult{Tool: a.Name(), Path: p.Path, Skipped: true, Error: err}
	}

	targeted, canTarget := a.(adapter.Targeted)
	if c == nil || !canTarget {
		return runFull(a, p)
	}

	stale, cached, ok := c.Lookup(a.Name(), p)
	if !ok {
		return runFull(a, p)
	}
	if len(stale) == 0 {
		// Everything this tool would look at is unchanged since the last
		// run; the tool is not invoked at all.
		return ToolResult{Tool: a.Name(), Path: p.Path, Findings: cached}
	}

	fresh, err := targeted.RunTargets(p.Path, stale)
	if err != nil {
		return ToolResult{Tool: a.Name(), Path: p.Path, Skipped: true, Error: err}
	}
	normalizeFilePaths(fresh, p.Path)
	c.Store(a.Name(), p, stale, fresh)
	return ToolResult{Tool: a.Name(), Path: p.Path, Findings: append(cached, fresh...)}
}

func runFull(a adapter.ToolAdapter, p detect.Project) ToolResult {
	findings, err := a.Run(p.Path)
	if err != nil {
		return ToolResult{Tool: a.Name(), Path: p.Path, Skipped: true, Error: err}
	}
	normalizeFilePaths(findings, p.Path)
	return ToolResult{Tool: a.Name(), Path: p.Path, Findings: findings}
}
```

If `internal/orchestrator/orchestrator_test.go` calls `runOne` directly, update those call sites to the new `(a, detect.Project{...}, inst, nil)` signature. Every existing assertion must still pass unchanged — `Run`'s behaviour has not moved.

- [ ] **Step 10: Wire the cache into the analyze handler**

In `internal/mcpserver/analyze.go`, replace

```go
	findings, skipped := collect(orchestrator.Run(projects, s.adapters))
```

with

```go
	findings, skipped := s.runCached(path, projects)
```

and add at the bottom of the file:

```go
// runCached runs every adapter for every project, serving unchanged
// package/crate results from disk. A cache that cannot be opened degrades to
// a full uncached run: a broken cache must never stop an analysis.
func (s *Server) runCached(rootPath string, projects []detect.Project) ([]finding.Finding, []SkippedTool) {
	store, err := cache.Open(rootPath)
	if err != nil {
		return collect(orchestrator.Run(projects, s.adapters))
	}
	defer store.Save()
	return collect(orchestrator.RunWithCache(projects, s.adapters, analysisCache{store: store}))
}

// analysisCache adapts the on-disk store to what the orchestrator needs.
type analysisCache struct{ store *cache.Store }

func (a analysisCache) Lookup(tool string, p detect.Project) (stale []string, cached []finding.Finding, ok bool) {
	units, err := cache.Units(p)
	if err != nil {
		return nil, nil, false
	}
	stamp := cache.ToolStamp(tool)
	for _, u := range units {
		fp, err := cache.Fingerprint(u.Dir, u.Exts)
		if err != nil {
			// One unreadable unit disables caching for this whole pair
			// rather than silently omitting it from the run.
			return nil, nil, false
		}
		if hit, found := a.store.Get(tool, stamp, u.Target, fp); found {
			cached = append(cached, hit...)
			continue
		}
		stale = append(stale, u.Target)
	}
	return stale, cached, true
}

func (a analysisCache) Store(tool string, p detect.Project, ran []string, produced []finding.Finding) {
	units, err := cache.Units(p)
	if err != nil {
		return
	}
	byTarget := map[string]cache.Unit{}
	for _, u := range units {
		byTarget[u.Target] = u
	}
	grouped := map[string][]finding.Finding{}
	for _, f := range produced {
		target := unitFor(f, units, p.Path)
		grouped[target] = append(grouped[target], f)
	}
	stamp := cache.ToolStamp(tool)
	for _, target := range ran {
		u, ok := byTarget[target]
		if !ok {
			continue
		}
		fp, err := cache.Fingerprint(u.Dir, u.Exts)
		if err != nil {
			continue
		}
		// Recorded even when grouped[target] is empty: "this package is
		// clean" is the common result and the one most worth caching.
		_ = a.store.Put(tool, stamp, target, fp, grouped[target])
	}
}

// unitFor maps a finding back to the unit that owns it, by matching its
// file's directory against the unit directories. A finding that matches
// nothing (a tool-level diagnostic with no file) is attributed to the project
// root so it is not silently dropped from the cache.
func unitFor(f finding.Finding, units []cache.Unit, root string) string {
	dir := filepath.Dir(filepath.Join(root, f.File))
	for _, u := range units {
		if u.Dir == dir {
			return u.Target
		}
	}
	return "./"
}
```

Add `"path/filepath"` and `"codebase-analyser/internal/cache"` to the imports.

- [ ] **Step 10b: Prove a cache hit is not "incomplete coverage"**

This is the sharpest edge in this codebase and it must have its own test. `cli.Execute` derives its **exit code 2 — "analysis incomplete, findings not trustworthy"** from `ToolResult.Skipped`, distinct from 1 (findings at/above threshold) and 0 (clean). The MCP server's `Incomplete`/`SkippedTools` output comes from the same signal.

A cache hit is a **complete** run, not a skipped one. If `RunWithCache` ever returns a skipped-shaped result for a cache hit, every cached run exits 2 and CI reports a healthy repository as a broken scan. The inverse must hold too: a genuinely skipped tool must still surface as skipped even when its neighbours all hit cache.

Add to `internal/mcpserver/analyze_test.go`:

```go
func TestCacheHitIsNotIncompleteCoverage(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	dir := goRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var runs [][]string
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {countingAdapter{
			fakeAdapter: fakeAdapter{name: "fake", findings: []finding.Finding{
				{File: "a.go", Line: 1, Tool: "fake", RuleID: "R1",
					Category: finding.CategoryCorrectness, Severity: finding.SeverityHigh, Message: "m"},
			}},
			runs: &runs,
		}},
	})
	cs := connect(t, s)

	first, _ := callAnalyze(t, cs, map[string]any{"path": dir})
	if first.Incomplete || len(first.SkippedTools) != 0 {
		t.Fatalf("first call: Incomplete=%v SkippedTools=%+v, want complete", first.Incomplete, first.SkippedTools)
	}

	second, _ := callAnalyze(t, cs, map[string]any{"path": dir})
	if second.Incomplete {
		t.Error("a fully cached run reported Incomplete=true; cli.Execute would exit 2 and CI would call a healthy repo a broken scan")
	}
	if len(second.SkippedTools) != 0 {
		t.Errorf("a cached run produced skip records: %+v", second.SkippedTools)
	}
	if second.Total != 1 {
		t.Errorf("cached run Total = %d, want 1", second.Total)
	}
}

func TestGenuineSkipStillSurfacesAlongsideCacheHits(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	dir := goRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var runs [][]string
	s := mcpserver.New(map[string][]adapter.ToolAdapter{
		"go": {
			countingAdapter{
				fakeAdapter: fakeAdapter{name: "cacheable", findings: []finding.Finding{
					{File: "a.go", Line: 1, Tool: "cacheable", RuleID: "R1",
						Category: finding.CategoryCorrectness, Severity: finding.SeverityHigh, Message: "m"},
				}},
				runs: &runs,
			},
			fakeAdapter{name: "broken", err: errFake}, // not Targeted: always runs, always fails
		},
	})
	cs := connect(t, s)

	callAnalyze(t, cs, map[string]any{"path": dir}) // warm the cache
	second, _ := callAnalyze(t, cs, map[string]any{"path": dir})

	if !second.Incomplete {
		t.Error("Incomplete=false, but a tool genuinely failed; a cache hit on its neighbour must not mask that")
	}
	found := false
	for _, s := range second.SkippedTools {
		if s.Tool == "broken" {
			found = true
		}
	}
	if !found {
		t.Errorf("the genuinely skipped tool vanished from SkippedTools: %+v", second.SkippedTools)
	}
}
```

Run: `go test ./internal/mcpserver/ -run 'TestCacheHitIsNot|TestGenuineSkip' -v`
Expected: PASS. If `TestCacheHitIsNotIncompleteCoverage` fails, the cache-hit branch in `runOne` is setting `Skipped` — it must return `ToolResult{Tool, Path, Findings}` with `Skipped` left false.

- [ ] **Step 11: Run everything**

Run: `go build ./... && go test ./...`
Expected: PASS, including every existing orchestrator and adapter test — `Run` behaves exactly as before, it is now `RunWithCache(..., nil)`.

Also confirm the three invariants the orchestrator already relies on survived the refactor, by reading your own diff:
- **Install memoization is still shared across projects.** `inst := newInstaller()` stays at the `RunWithCache` level, one per call, not one per project. Two Go projects must install `gosec` once between them, with every waiter observing the same outcome including failure.
- **Path normalization still happens on every findings path.** `normalizeFilePaths` must run in `runFull` AND on the fresh findings in the targeted branch. gosec emits absolute paths and golangci-lint relative ones; missing this puts the same file under two names and breaks downstream grouping. Cached findings were normalized when stored and must not be normalized twice.
- **Panic recovery still fills in `Path`.** See the comment in `runOne`.

- [ ] **Step 12: Re-measure**

Repeat Task 5 Step 1's second measurement and report the before/after numbers. If the cache did not measurably help, say so plainly — that is a legitimate result and the reviewer may choose to revert Stage 2.

- [ ] **Step 13: Report to the reviewer**

Then emit one single-line commit message and stop; the user commits and pushes before the next task starts.

---

## Stage 3 — Toolchain resolution

### Task 7: `Resolver` interface + Go resolver — tier: `sonnet`

Running the wrong language version risks false positives and negatives (spec: Compatibility). Go already has the mechanism for this: `GOTOOLCHAIN=go1.2.3` makes any Go 1.21+ command download and use that exact version. Task 7 detects the version and sets the variable. Task 9 handles the machine with no Go at all.

**Files:**
- Create: `internal/toolchain/toolchain.go`
- Create: `internal/toolchain/golang.go`
- Create: `internal/toolchain/golang_test.go`
- Modify: `internal/adapter/adapter.go` (add the `EnvForPath` hook)
- Modify: `internal/mcpserver/server.go` (install the hook)

**Interfaces:**
- Produces:
  - `toolchain.Resolver` interface: `Detect(repoPath string) (version string, ok bool)`, `Ensure(version string) (env []string, err error)`
  - `toolchain.Go` implementing it
  - `toolchain.Env(repoPath string) []string` — the aggregate every adapter run uses
  - `adapter.EnvForPath func(path string) []string` — package-level hook, default returns nil

- [ ] **Step 1: Write the failing test**

Create `internal/toolchain/golang_test.go`:

```go
package toolchain_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"codebase-analyser/internal/toolchain"
)

func writeMod(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGoDetectReadsTheGoDirective(t *testing.T) {
	cases := []struct {
		name, mod, want string
		wantOK          bool
	}{
		{"patch version", "module m\n\ngo 1.26.5\n", "1.26.5", true},
		{"minor version", "module m\n\ngo 1.22\n", "1.22", true},
		{"toolchain line is not the go directive", "module m\n\ngo 1.22\n\ntoolchain go1.26.5\n", "1.22", true},
		{"tabs and trailing comment", "module m\n\ngo\t1.23.1 // pinned\n", "1.23.1", true},
		{"no directive", "module m\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toolchain.Go{}.Detect(writeMod(t, tc.mod))
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("Detect = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestGoDetectOnANonGoDirectory(t *testing.T) {
	if _, ok := toolchain.Go{}.Detect(t.TempDir()); ok {
		t.Error("ok = true for a directory with no go.mod")
	}
}

func TestGoEnsureSetsGOTOOLCHAIN(t *testing.T) {
	env, err := toolchain.Go{}.Ensure("1.26.5")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "GOTOOLCHAIN=go1.26.5") {
		t.Errorf("env = %v, want it to contain GOTOOLCHAIN=go1.26.5", env)
	}
}

func TestEnvForARepoWithNoDeclaredVersionIsEmpty(t *testing.T) {
	if env := toolchain.Env(t.TempDir()); len(env) != 0 {
		t.Errorf("Env = %v, want empty when nothing is declared (fall back to whatever is installed)", env)
	}
}

func TestEnvForAGoRepo(t *testing.T) {
	dir := writeMod(t, "module m\n\ngo 1.26.5\n")
	if env := toolchain.Env(dir); !slices.Contains(env, "GOTOOLCHAIN=go1.26.5") {
		t.Errorf("Env = %v, want GOTOOLCHAIN=go1.26.5", env)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/toolchain/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write `internal/toolchain/toolchain.go`**

```go
// Package toolchain makes each analysis run against the language version the
// repository declares, rather than whatever happens to be installed.
// Analysing with the wrong version produces false positives (valid syntax
// flagged) and false negatives (a real deprecation missed).
package toolchain

// Resolver detects the language version a repository declares and produces
// the environment needed to run tools at that version. It is an interface
// because Java, Python, JS/TS and Next.js support are on the roadmap: adding
// one is a new implementation of this shape, not a rework.
type Resolver interface {
	// Detect reads the repository's declared version (go.mod's go directive,
	// rust-toolchain.toml, ...). ok is false when nothing is declared, which
	// means "use the latest stable / whatever is installed".
	Detect(repoPath string) (version string, ok bool)

	// Ensure returns the environment variables that make tools run at
	// version, downloading the toolchain first if that is what it takes.
	Ensure(version string) (env []string, err error)
}

// resolvers is every language this build knows about. Go and Rust ship in
// v1; the interface above is what keeps adding a third from being a rewrite.
var resolvers = []Resolver{Go{}, Rust{}}

// Env returns the extra environment for running tools against repoPath. A
// repository that declares nothing gets an empty environment: falling back to
// the installed toolchain is correct, and quieter than guessing.
//
// A resolver that fails to Ensure is skipped rather than fatal: running the
// analysis at the wrong version still finds real bugs, whereas not running it
// at all finds none.
func Env(repoPath string) []string {
	var env []string
	for _, r := range resolvers {
		version, ok := r.Detect(repoPath)
		if !ok {
			continue
		}
		vars, err := r.Ensure(version)
		if err != nil {
			continue
		}
		env = append(env, vars...)
	}
	return env
}
```

- [ ] **Step 4: Write `internal/toolchain/golang.go`**

```go
package toolchain

import (
	"os"
	"path/filepath"
	"regexp"
)

// goDirective matches go.mod's `go` directive and nothing else - notably not
// the `toolchain go1.x.y` line, which is a different statement with a
// different meaning.
var goDirective = regexp.MustCompile(`(?m)^go[ \t]+([0-9]+(?:\.[0-9]+){1,2}(?:(?:rc|beta)[0-9]+)?)`)

// Go resolves the version declared by a repository's go.mod.
type Go struct{}

func (Go) Detect(repoPath string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return "", false
	}
	m := goDirective.FindSubmatch(b)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

// Ensure leans on Go's own toolchain switching: with GOTOOLCHAIN set to an
// explicit version, any Go 1.21+ command downloads and runs that version,
// caching it in the module cache. That is a supported, signed download path -
// there is no reason to reimplement it.
func (Go) Ensure(version string) ([]string, error) {
	return []string{"GOTOOLCHAIN=go" + version}, nil
}
```

Also create a placeholder-free `Rust` so the package compiles before Task 8 lands. Create `internal/toolchain/rust.go`:

```go
package toolchain

// Rust resolves the toolchain declared by rust-toolchain.toml. Task 8 fills
// in Detect; until then a Rust repository simply uses the installed
// toolchain, which is the same behaviour the analyser has today.
type Rust struct{}

func (Rust) Detect(repoPath string) (string, bool) { return "", false }

func (Rust) Ensure(version string) ([]string, error) { return nil, nil }
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/toolchain/... -v`
Expected: PASS.

- [ ] **Step 6: Add the env hook to the adapter package**

In `internal/adapter/adapter.go`, above `runCommand`:

```go
// EnvForPath supplies extra environment variables for tool runs against a
// project path - the toolchain package sets it so each repo is analysed at
// the language version it declares. Default is nil, which leaves tool runs
// exactly as they were.
//
// ponytail: one package-level hook because there is exactly one producer
// (internal/toolchain) and every adapter needs it; make it a field on each
// adapter if a second producer ever needs different behaviour per tool.
var EnvForPath = func(path string) []string { return nil }
```

and in `runCommand`, immediately after `cmd.Dir = dir`:

```go
	if extra := EnvForPath(dir); len(extra) > 0 {
		cmd.Env = append(os.Environ(), extra...)
	}
```

- [ ] **Step 7: Test the hook**

Append to `internal/adapter/adapter_test.go`:

```go
func TestEnvForPathReachesTheToolProcess(t *testing.T) {
	original := adapter.EnvForPath
	t.Cleanup(func() { adapter.EnvForPath = original })
	adapter.EnvForPath = func(string) []string { return []string{"CODEBASE_ANALYSER_PROBE=set"} }

	// Uses `env`, which is present on every platform this test runs on.
	// (If adapter_test.go is an internal test, call runCommand directly;
	// if it is an external _test package, exercise it through whichever
	// existing helper the file already uses to invoke a command.)
	out, err := runCommand(t.TempDir(), "env")
	if err != nil {
		t.Skipf("env not available: %v", err)
	}
	if !strings.Contains(string(out), "CODEBASE_ANALYSER_PROBE=set") {
		t.Error("EnvForPath variables did not reach the tool process")
	}
}
```

Match the file's existing package declaration and imports; if `adapter_test.go` is `package adapter_test`, move this test into a new internal test file `internal/adapter/env_internal_test.go` with `package adapter` so it can call `runCommand`.

- [ ] **Step 8: Install the hook in the MCP server**

In `internal/mcpserver/server.go`, inside `New`, before returning:

```go
	// Analyse each repository at the language version it declares.
	adapter.EnvForPath = toolchain.Env
```

Add `"codebase-analyser/internal/toolchain"` to the imports.

- [ ] **Step 9: Run everything**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 10: Report to the reviewer**

Then emit one single-line commit message and stop; the user commits and pushes before the next task starts.

---

### Task 8: Rust resolver — tier: `haiku`

**Files:**
- Modify: `internal/toolchain/rust.go`
- Create: `internal/toolchain/rust_test.go`

**Interfaces:**
- Consumes: Task 7's `Resolver` interface and the `Rust` stub.
- Produces: a working `toolchain.Rust`.

`rust-toolchain.toml` has two accepted forms — the modern table and a legacy bare-string file:

```toml
[toolchain]
channel = "1.78.0"
```
```
1.78.0
```

`RUSTUP_TOOLCHAIN` makes rustup use (and install, if missing) exactly that toolchain for any `cargo`/`clippy` invocation, which is the same trick Task 7 uses for Go.

- [ ] **Step 1: Write the failing test**

Create `internal/toolchain/rust_test.go`:

```go
package toolchain_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"codebase-analyser/internal/toolchain"
)

func writeToolchainFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRustDetect(t *testing.T) {
	cases := []struct {
		name, file, content, want string
		wantOK                    bool
	}{
		{"table form", "rust-toolchain.toml", "[toolchain]\nchannel = \"1.78.0\"\n", "1.78.0", true},
		{"table form, single quotes", "rust-toolchain.toml", "[toolchain]\nchannel = '1.78.0'\n", "1.78.0", true},
		{"table form with components", "rust-toolchain.toml", "[toolchain]\nchannel = \"stable\"\ncomponents = [\"clippy\"]\n", "stable", true},
		{"legacy bare file", "rust-toolchain", "1.78.0\n", "1.78.0", true},
		{"no channel key", "rust-toolchain.toml", "[toolchain]\ncomponents = [\"clippy\"]\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toolchain.Rust{}.Detect(writeToolchainFile(t, tc.file, tc.content))
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("Detect = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestRustDetectWithNoToolchainFile(t *testing.T) {
	// Parenthesised: a composite literal in an if-init clause is parsed as
	// the start of the block, so `toolchain.Rust{}.Detect(...)` there is a
	// compile error. Same applies anywhere else in this plan.
	if _, ok := (toolchain.Rust{}).Detect(t.TempDir()); ok {
		t.Error("ok = true with no rust-toolchain file present")
	}
}

func TestRustEnsureSetsRUSTUPTOOLCHAIN(t *testing.T) {
	env, err := toolchain.Rust{}.Ensure("1.78.0")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "RUSTUP_TOOLCHAIN=1.78.0") {
		t.Errorf("env = %v, want RUSTUP_TOOLCHAIN=1.78.0", env)
	}
}

func TestEnvForARustRepo(t *testing.T) {
	dir := writeToolchainFile(t, "rust-toolchain.toml", "[toolchain]\nchannel = \"1.78.0\"\n")
	if env := toolchain.Env(dir); !slices.Contains(env, "RUSTUP_TOOLCHAIN=1.78.0") {
		t.Errorf("Env = %v, want RUSTUP_TOOLCHAIN=1.78.0", env)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/toolchain/ -run Rust -v`
Expected: FAIL — `Detect` always returns `("", false)`.

- [ ] **Step 3: Implement `internal/toolchain/rust.go`**

```go
package toolchain

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// channelKey matches the `channel = "..."` entry of rust-toolchain.toml.
// A three-line regex beats a TOML dependency for one key in one file.
var channelKey = regexp.MustCompile(`(?m)^[ \t]*channel[ \t]*=[ \t]*["']([^"']+)["']`)

// Rust resolves the toolchain declared by rust-toolchain.toml, or by the
// legacy bare `rust-toolchain` file.
type Rust struct{}

func (Rust) Detect(repoPath string) (string, bool) {
	if b, err := os.ReadFile(filepath.Join(repoPath, "rust-toolchain.toml")); err == nil {
		if m := channelKey.FindSubmatch(b); m != nil {
			return string(m[1]), true
		}
		return "", false
	}
	// The legacy file is the bare toolchain name, nothing else.
	b, err := os.ReadFile(filepath.Join(repoPath, "rust-toolchain"))
	if err != nil {
		return "", false
	}
	name := strings.TrimSpace(string(b))
	if name == "" || strings.ContainsAny(name, "\n[=") {
		return "", false
	}
	return name, true
}

// Ensure leans on rustup, which installs a missing toolchain on first use
// when RUSTUP_TOOLCHAIN names it. Same reasoning as Go's GOTOOLCHAIN: the
// language ships this machinery, so we do not rebuild it.
func (Rust) Ensure(version string) ([]string, error) {
	return []string{"RUSTUP_TOOLCHAIN=" + version}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/toolchain/... -v`
Expected: PASS (Go and Rust suites).

- [ ] **Step 5: Run everything**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Report to the reviewer**

Then emit one single-line commit message and stop; the user commits and pushes before the next task starts.

---

### Task 9: Bootstrap Go and Rust when neither is installed — tier: `sonnet`

Tasks 7 and 8 cover "the repo declares a version" on a machine that already has *some* Go/Rust. This task covers the spec's stronger claim: **no system Go/Rust toolchain required** — the analyser downloads and manages its own, never touching the user's install, the way `rustup` and `nvm` do.

**Files:**
- Create: `internal/toolchain/bootstrap.go`
- Create: `internal/toolchain/bootstrap_test.go`
- Modify: `internal/toolchain/golang.go`, `internal/toolchain/rust.go` (call the bootstrap from `Ensure`)

**Interfaces:**
- Consumes: Task 7's `Resolver`.
- Produces: `toolchain.EnsureGo(version string) (goroot string, err error)`; `toolchain.EnsureRustup() (cargoHome string, err error)`.

Layout, all under `os.UserCacheDir()/codebase-analyser/toolchains/`:
- Go: `go/<version>/` containing the extracted archive (its `bin/go` is the binary).
- Rust: `rust/` used as both `RUSTUP_HOME` and `CARGO_HOME`, populated by `rustup-init -y --no-modify-path`.

Download sources:
- Go: `https://go.dev/dl/go<version>.<goos>-<goarch>.tar.gz` (`.zip` on Windows), checksum at the same URL plus `.sha256`.
- rustup: `https://static.rust-lang.org/rustup/dist/<target-triple>/rustup-init` (`.exe` on Windows).

- [ ] **Step 1: Write the failing test**

Create `internal/toolchain/bootstrap_test.go`. Network downloads are not exercised in unit tests; what is tested is that an already-populated cache is reused without any network access, and that a checksum mismatch is rejected.

```go
package toolchain_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codebase-analyser/internal/toolchain"
)

func TestEnsureGoReusesAnAlreadyExtractedToolchain(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("CODEBASE_ANALYSER_CACHE", cacheHome)

	// Pre-populate the cache the way a previous run would have.
	root := filepath.Join(cacheHome, "toolchains", "go", "1.26.5")
	bin := filepath.Join(root, "go", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bin, "go")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Point downloads at an address that cannot resolve: a cache hit must
	// never touch the network.
	t.Setenv("CODEBASE_ANALYSER_GO_DL", "http://127.0.0.1:1/")

	goroot, err := toolchain.EnsureGo("1.26.5")
	if err != nil {
		t.Fatalf("EnsureGo on a warm cache: %v", err)
	}
	if !strings.HasPrefix(goroot, root) {
		t.Errorf("goroot = %q, want it inside %q", goroot, root)
	}
}

func TestEnsureGoRejectsAChecksumMismatch(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	// A server that serves a body and a checksum that do not match.
	srv := newMismatchServer(t)
	t.Setenv("CODEBASE_ANALYSER_GO_DL", srv.URL+"/")

	if _, err := toolchain.EnsureGo("1.26.5"); err == nil {
		t.Fatal("err = nil, want a checksum-mismatch error")
	} else if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("err = %v, want it to name the checksum mismatch", err)
	}
}
```

Write `newMismatchServer` as an `httptest.Server` in the same file: it serves any `*.sha256` path as the fixed string `"0000000000000000000000000000000000000000000000000000000000000000"` and any other path as the bytes `"not-a-real-archive"`.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/toolchain/ -run EnsureGo -v`
Expected: FAIL — `undefined: toolchain.EnsureGo`.

- [ ] **Step 3: Write `internal/toolchain/bootstrap.go`**

```go
package toolchain

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"codebase-analyser/internal/cache"
)

// Download bases are read from the environment so tests can point them at a
// local server. They are env vars rather than package variables because a
// test that spawns the server as a subprocess needs them to cross that
// boundary.
func goDownloadBase() string {
	if base := os.Getenv("CODEBASE_ANALYSER_GO_DL"); base != "" {
		return base
	}
	return "https://go.dev/dl/"
}

func rustupDownloadBase() string {
	if base := os.Getenv("CODEBASE_ANALYSER_RUSTUP_DL"); base != "" {
		return base
	}
	return "https://static.rust-lang.org/rustup/dist/"
}

// toolchainsDir is where downloaded toolchains live, alongside the analysis
// cache and under the same CODEBASE_ANALYSER_CACHE override.
func toolchainsDir() (string, error) {
	root, err := cache.Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "toolchains"), nil
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// EnsureGo returns the GOROOT of a Go toolchain we manage ourselves,
// downloading and extracting it on first use. The user's own Go install is
// never touched.
func EnsureGo(version string) (string, error) {
	dir, err := toolchainsDir()
	if err != nil {
		return "", err
	}
	// The official archive contains a single top-level "go" directory, so
	// the extracted GOROOT sits one level inside the versioned root.
	root := filepath.Join(dir, "go", version)
	goroot := filepath.Join(root, "go")
	if isExecutable(filepath.Join(goroot, "bin", exeName("go"))) {
		return goroot, nil
	}

	unlock, err := lock(dir, "go-"+version)
	if err != nil {
		return "", err
	}
	defer unlock()
	// Another process may have finished while we waited for the lock.
	if isExecutable(filepath.Join(goroot, "bin", exeName("go"))) {
		return goroot, nil
	}

	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	url := fmt.Sprintf("%sgo%s.%s-%s%s", goDownloadBase(), version, runtime.GOOS, runtime.GOARCH, ext)

	// stderr, never stdout: this can run inside an MCP session where stdout
	// carries the JSON-RPC stream.
	fmt.Fprintf(os.Stderr, "codebase-analyser: downloading Go %s for %s/%s (first run only)...\n",
		version, runtime.GOOS, runtime.GOARCH)

	archive, err := download(url, url+".sha256")
	if err != nil {
		return "", err
	}
	defer os.Remove(archive)

	// Extract to a staging directory and rename into place, so an
	// interrupted run never leaves a half-extracted toolchain that the
	// cache-hit check above would then accept.
	staging := root + ".staging"
	os.RemoveAll(staging)
	if err := extractTarGz(archive, staging); err != nil {
		os.RemoveAll(staging)
		return "", err
	}
	os.RemoveAll(root)
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(staging, root); err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "codebase-analyser: Go %s ready\n", version)
	return goroot, nil
}

// EnsureRustup returns a CARGO_HOME/RUSTUP_HOME we populate with rustup-init.
// The user's own ~/.cargo and ~/.rustup are never touched.
func EnsureRustup() (string, error) {
	dir, err := toolchainsDir()
	if err != nil {
		return "", err
	}
	home := filepath.Join(dir, "rust")
	if isExecutable(filepath.Join(home, "bin", exeName("cargo"))) {
		return home, nil
	}

	unlock, err := lock(dir, "rust")
	if err != nil {
		return "", err
	}
	defer unlock()
	if isExecutable(filepath.Join(home, "bin", exeName("cargo"))) {
		return home, nil
	}

	triple, err := rustTargetTriple()
	if err != nil {
		return "", err
	}
	url := rustupDownloadBase() + triple + "/" + exeName("rustup-init")
	fmt.Fprintln(os.Stderr, "codebase-analyser: installing an isolated Rust toolchain (first run only)...")

	init, err := download(url, url+".sha256")
	if err != nil {
		return "", err
	}
	defer os.Remove(init)
	if err := os.Chmod(init, 0o755); err != nil {
		return "", err
	}

	cmd := exec.Command(init, "-y", "--no-modify-path", "--profile", "minimal",
		"--default-toolchain", "stable", "-c", "clippy")
	cmd.Env = append(os.Environ(), "RUSTUP_HOME="+home, "CARGO_HOME="+home)
	cmd.Stdout = os.Stderr // rustup's progress output must not reach stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("rustup-init: %w", err)
	}
	fmt.Fprintln(os.Stderr, "codebase-analyser: Rust toolchain ready")
	return home, nil
}

func rustTargetTriple() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "x86_64-unknown-linux-gnu", nil
	case "linux/arm64":
		return "aarch64-unknown-linux-gnu", nil
	case "darwin/amd64":
		return "x86_64-apple-darwin", nil
	case "darwin/arm64":
		return "aarch64-apple-darwin", nil
	case "windows/amd64":
		return "x86_64-pc-windows-msvc", nil
	}
	return "", fmt.Errorf("no rustup build for %s/%s", runtime.GOOS, runtime.GOARCH)
}

// download fetches url to a temp file, verifying it against the checksum
// served at sumURL. Nothing is ever extracted or executed before the hash
// matches.
func download(url, sumURL string) (string, error) {
	want, err := fetchChecksum(sumURL)
	if err != nil {
		return "", err
	}

	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	f, err := os.CreateTemp("", "codebase-analyser-dl-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, h), resp.Body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("download %s: %w", url, errOr(copyErr, closeErr))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(f.Name())
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, got, want)
	}
	return f.Name(), nil
}

func errOr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func fetchChecksum(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum at %s", url)
	}
	return strings.ToLower(fields[0]), nil
}

// safeJoin rejects archive entries that would write outside dest. An archive
// is untrusted input until proven otherwise, and "../.." in an entry name is
// the classic way out of an extraction directory.
func safeJoin(dest, name string) (string, error) {
	target := filepath.Join(dest, filepath.FromSlash(name))
	prefix := filepath.Clean(dest) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), prefix) {
		return "", fmt.Errorf("archive entry escapes destination: %q", name)
	}
	return target, nil
}

func extractTarGz(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			// Symlinks and devices are skipped: the Go archive has none that
			// matter, and a symlink is the easiest way to write outside dest.
		}
	}
}

// lock serializes bootstraps across processes, so two analyses starting at
// once cannot extract into the same directory. A lock left behind by a killed
// process is reclaimed once it is clearly stale.
func lock(dir, name string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".lock")
	deadline := time.Now().Add(10 * time.Minute)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 15*time.Minute {
			os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for %s", path)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
```

Windows note: `EnsureGo` fetches a `.zip` there but `extractTarGz` cannot read it. Add an `extractZip` using stdlib `archive/zip` with the same `safeJoin` check and dispatch on `runtime.GOOS == "windows"`, or — if you cannot test it — return a clear `fmt.Errorf("automatic Go bootstrap is not supported on Windows yet; install Go and re-run")` from `EnsureGo` on Windows and say so in your report. Do not ship an untested zip path silently.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/toolchain/ -run Ensure -v`
Expected: PASS.

- [ ] **Step 5: Fall back to the bootstrap in the resolvers**

In `internal/toolchain/golang.go`, replace `Ensure`:

```go
// Ensure prefers Go's own toolchain switching, which needs any Go 1.21+ on
// PATH. With no Go at all, it falls back to a Go we download and manage
// ourselves, so the analyser works on a machine with no Go installed.
func (Go) Ensure(version string) ([]string, error) {
	if _, err := exec.LookPath("go"); err == nil {
		return []string{"GOTOOLCHAIN=go" + version}, nil
	}
	goroot, err := EnsureGo(version)
	if err != nil {
		return nil, err
	}
	return []string{
		"GOROOT=" + goroot,
		"PATH=" + filepath.Join(goroot, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, nil
}
```

Apply the mirror-image change in `internal/toolchain/rust.go`: if `rustup` is on PATH, keep returning `RUSTUP_TOOLCHAIN=<version>`; otherwise call `EnsureRustup` and return `RUSTUP_HOME`, `CARGO_HOME`, an extended `PATH`, and `RUSTUP_TOOLCHAIN=<version>`.

- [ ] **Step 6: Verify the Task 7 and 8 tests still hold**

Run: `go test ./internal/toolchain/... -v`
Expected: PASS. `TestGoEnsureSetsGOTOOLCHAIN` and `TestRustEnsureSetsRUSTUPTOOLCHAIN` pass on any machine with Go/rustup installed, which is every machine running these tests.

- [ ] **Step 7: Run everything**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 8: Report to the reviewer**

Note explicitly which requirement of Step 3 you verified with a test and which you verified only by reading. Then emit one single-line commit message and stop; the user commits and pushes before the next task starts.

---

## Stage 4 — Distribution

### Task 10: Cross-compiled release pipeline — tier: `haiku`

**Files:**
- Create: `.github/workflows/release.yml`

**Interfaces:**
- Produces: the release-asset naming contract Task 11 consumes:
  - `codebase-analyser-mcp_<version>_<os>_<arch>[.exe]` where `<version>` is the tag without its leading `v`, `<os>` ∈ `darwin | linux | windows`, `<arch>` ∈ `amd64 | arm64`
  - `checksums.txt` — one `sha256  filename` line per asset, the format `sha256sum -c` reads
  - Five assets: `darwin_arm64`, `darwin_amd64`, `linux_arm64`, `linux_amd64`, `windows_amd64.exe` (spec: Compatibility)

- [ ] **Step 1: Write `.github/workflows/release.yml`**

```yaml
name: release

on:
  push:
    tags: ['v*']

permissions:
  contents: write

jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - goos: darwin
            goarch: arm64
          - goos: darwin
            goarch: amd64
          - goos: linux
            goarch: arm64
          - goos: linux
            goarch: amd64
          - goos: windows
            goarch: amd64
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Build
        env:
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
          CGO_ENABLED: '0'
        run: |
          version="${GITHUB_REF_NAME#v}"
          name="codebase-analyser-mcp_${version}_${GOOS}_${GOARCH}"
          if [ "$GOOS" = "windows" ]; then name="${name}.exe"; fi
          mkdir -p dist
          go build -trimpath -ldflags "-s -w -X codebase-analyser/internal/mcpserver.Version=${version}" \
            -o "dist/${name}" ./cmd/codebase-analyser-mcp

      - uses: actions/upload-artifact@v4
        with:
          name: dist-${{ matrix.goos }}-${{ matrix.goarch }}
          path: dist/

  release:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@v4
        with:
          path: dist
          merge-multiple: true

      # One checksums file for all five assets: the npm wrapper verifies the
      # binary it downloaded against this before ever executing it.
      - name: Checksums
        run: cd dist && sha256sum * > checksums.txt && cat checksums.txt

      - uses: softprops/action-gh-release@v2
        with:
          files: dist/*
          fail_on_unmatched_files: true
```

- [ ] **Step 2: Verify every matrix target actually compiles**

Run locally:

```bash
for pair in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64; do
  GOOS=${pair%/*} GOARCH=${pair#*/} CGO_ENABLED=0 \
    go build -o /dev/null ./cmd/codebase-analyser-mcp && echo "ok $pair" || echo "FAIL $pair"
done
```
Expected: `ok` for all five. `/dev/null` as an output path works on Linux and macOS; on Windows build into a temp directory instead.

- [ ] **Step 3: Verify the version ldflag path is real**

Run: `go build -ldflags "-X codebase-analyser/internal/mcpserver.Version=9.9.9" -o /tmp/ca-mcp ./cmd/codebase-analyser-mcp`
Then run `/tmp/ca-mcp` and confirm it starts (it will block waiting for JSON-RPC on stdin; Ctrl-C is fine).
Expected: builds with no `-X` warning. Task 1 declares `Version` as a `var` precisely so this works — if Go warns that the symbol was not found, the import path in the workflow's `-ldflags` does not match `internal/mcpserver`.

- [ ] **Step 4: Report to the reviewer**

The workflow cannot be end-to-end verified without pushing a tag, which is the user's call. Say so. Then emit one single-line commit message and stop; the user commits, pushes, and decides when to tag.

---

### Task 11: npm wrapper package — tier: `haiku`

`npx -y @codebase-analyser/mcp` must be the single line an agent's MCP config needs (spec: Architecture). The wrapper detects the platform, downloads the matching prebuilt binary from GitHub Releases, caches it, verifies it, and execs it with stdio passed straight through.

**Files:**
- Create: `npm/package.json`
- Create: `npm/index.js`
- Create: `npm/index.test.js`
- Create: `npm/README.md`
- Modify: `docs/mcp-server.md` (swap the `command` in all three config snippets to the npx form)

**Interfaces:**
- Consumes: Task 10's asset naming and `checksums.txt`.
- Produces: nothing other tasks depend on.

Zero npm dependencies: Node's built-in `fetch`, `node:fs`, `node:child_process`, and `node:test` cover all of it. Adding a download or CLI library for this would be pure weight.

- [ ] **Step 1: Write `npm/package.json`**

```json
{
  "name": "@codebase-analyser/mcp",
  "version": "0.1.0",
  "description": "MCP server for codebase-analyser: static analysis for Go and Rust as an agent tool",
  "license": "MIT",
  "type": "module",
  "bin": {
    "codebase-analyser-mcp": "index.js"
  },
  "files": ["index.js", "README.md"],
  "engines": {
    "node": ">=20"
  },
  "scripts": {
    "test": "node --test"
  },
  "repository": {
    "type": "git",
    "url": "https://github.com/OWNER/codebase-analyser.git"
  }
}
```

Replace `OWNER` with the actual GitHub owner before publishing; if it is not yet known, leave `OWNER` and say so in your report rather than inventing one.

- [ ] **Step 2: Write the failing test**

Create `npm/index.test.js`:

```js
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { assetName, cachePath, parseChecksums } from './index.js'

test('assetName covers every released target', () => {
  assert.equal(assetName('1.2.3', 'darwin', 'arm64'), 'codebase-analyser-mcp_1.2.3_darwin_arm64')
  assert.equal(assetName('1.2.3', 'darwin', 'x64'), 'codebase-analyser-mcp_1.2.3_darwin_amd64')
  assert.equal(assetName('1.2.3', 'linux', 'arm64'), 'codebase-analyser-mcp_1.2.3_linux_arm64')
  assert.equal(assetName('1.2.3', 'linux', 'x64'), 'codebase-analyser-mcp_1.2.3_linux_amd64')
  assert.equal(assetName('1.2.3', 'win32', 'x64'), 'codebase-analyser-mcp_1.2.3_windows_amd64.exe')
})

test('assetName rejects an unreleased platform with an actionable message', () => {
  assert.throws(() => assetName('1.2.3', 'linux', 'ia32'), /unsupported/i)
  assert.throws(() => assetName('1.2.3', 'win32', 'arm64'), /unsupported/i)
})

test('cachePath is versioned so an upgrade does not reuse the old binary', () => {
  const a = cachePath('1.2.3', 'codebase-analyser-mcp_1.2.3_linux_amd64')
  const b = cachePath('1.2.4', 'codebase-analyser-mcp_1.2.4_linux_amd64')
  assert.notEqual(a, b)
  assert.match(a, /codebase-analyser/)
})

test('parseChecksums reads the sha256sum format', () => {
  const text = [
    'aaaa  codebase-analyser-mcp_1.2.3_linux_amd64',
    'bbbb  codebase-analyser-mcp_1.2.3_darwin_arm64',
    '',
  ].join('\n')
  const sums = parseChecksums(text)
  assert.equal(sums['codebase-analyser-mcp_1.2.3_linux_amd64'], 'aaaa')
  assert.equal(sums['codebase-analyser-mcp_1.2.3_darwin_arm64'], 'bbbb')
})
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd npm && node --test`
Expected: FAIL — `Cannot find module './index.js'`.

- [ ] **Step 4: Write `npm/index.js`**

```js
#!/usr/bin/env node
// Thin launcher for the codebase-analyser MCP server.
//
// On first run it downloads the prebuilt Go binary for this platform from
// GitHub Releases, verifies it against the release checksums, caches it, and
// execs it. Every later run is a cache hit and an exec. Same pattern esbuild
// and biome use to ship a compiled binary through npm.
//
// stdio is inherited, not piped: the parent process IS the MCP host, and the
// JSON-RPC stream must pass through untouched.

import { createHash } from 'node:crypto'
import { spawn } from 'node:child_process'
import { chmodSync, existsSync, mkdirSync, renameSync, writeFileSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { createRequire } from 'node:module'

const REPO = process.env.CODEBASE_ANALYSER_REPO ?? 'OWNER/codebase-analyser'
const VERSION = createRequire(import.meta.url)('./package.json').version

const ARCH = { arm64: 'arm64', x64: 'amd64' }
const OS = { darwin: 'darwin', linux: 'linux', win32: 'windows' }

// Windows is amd64-only in the release matrix; anything else is a build we
// do not publish, and saying so beats a 404.
export function assetName(version, platform, arch) {
  const goos = OS[platform]
  const goarch = ARCH[arch]
  if (!goos || !goarch || (goos === 'windows' && goarch !== 'amd64')) {
    throw new Error(
      `unsupported platform ${platform}/${arch}. Prebuilt binaries exist for ` +
        `darwin/arm64, darwin/amd64, linux/arm64, linux/amd64, windows/amd64. ` +
        `Build from source: go build ./cmd/codebase-analyser-mcp`,
    )
  }
  return `codebase-analyser-mcp_${version}_${goos}_${goarch}${goos === 'windows' ? '.exe' : ''}`
}

export function cachePath(version, name) {
  const base =
    process.env.XDG_CACHE_HOME ??
    (process.platform === 'darwin'
      ? join(homedir(), 'Library', 'Caches')
      : process.platform === 'win32'
        ? process.env.LOCALAPPDATA ?? join(homedir(), 'AppData', 'Local')
        : join(homedir(), '.cache'))
  return join(base, 'codebase-analyser', 'bin', version, name)
}

export function parseChecksums(text) {
  const out = {}
  for (const line of text.split('\n')) {
    const m = line.trim().match(/^([0-9a-f]+)\s+\*?(.+)$/i)
    if (m) out[m[2]] = m[1].toLowerCase()
  }
  return out
}

async function fetchOrThrow(url) {
  const res = await fetch(url, { redirect: 'follow' })
  if (!res.ok) throw new Error(`GET ${url}: ${res.status} ${res.statusText}`)
  return res
}

async function ensureBinary() {
  const name = assetName(VERSION, process.platform, process.arch)
  const dest = cachePath(VERSION, name)
  if (existsSync(dest)) return dest

  const base = `https://github.com/${REPO}/releases/download/v${VERSION}`
  process.stderr.write(`codebase-analyser-mcp: downloading ${name} (first run only)...\n`)

  const sums = parseChecksums(await (await fetchOrThrow(`${base}/checksums.txt`)).text())
  const want = sums[name]
  if (!want) throw new Error(`${name} is not listed in the release checksums`)

  const body = Buffer.from(await (await fetchOrThrow(`${base}/${name}`)).arrayBuffer())
  const got = createHash('sha256').update(body).digest('hex')
  if (got !== want) throw new Error(`checksum mismatch for ${name}: got ${got}, want ${want}`)

  // Write to a temp file and rename, so an interrupted download never leaves
  // a truncated binary that the next run treats as a cache hit.
  mkdirSync(dirname(dest), { recursive: true })
  const tmp = join(tmpdir(), `${name}.${process.pid}`)
  writeFileSync(tmp, body)
  chmodSync(tmp, 0o755)
  renameSync(tmp, dest)
  return dest
}

async function main() {
  let bin
  try {
    bin = await ensureBinary()
  } catch (err) {
    process.stderr.write(`codebase-analyser-mcp: ${err.message}\n`)
    process.exit(1)
  }
  const child = spawn(bin, process.argv.slice(2), { stdio: 'inherit' })
  child.on('exit', (code, signal) => process.exit(signal ? 1 : (code ?? 0)))
  child.on('error', (err) => {
    process.stderr.write(`codebase-analyser-mcp: ${err.message}\n`)
    process.exit(1)
  })
}

// Only run when invoked as the binary, so the tests can import the helpers.
if (process.argv[1] && process.argv[1].endsWith('index.js')) await main()
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd npm && node --test`
Expected: PASS (4 tests).

- [ ] **Step 6: Verify the cache-hit path execs a real binary**

```bash
cd npm
node --input-type=module -e "
import { assetName, cachePath } from './index.js'
import { readFileSync, mkdirSync, copyFileSync } from 'node:fs'
import { dirname } from 'node:path'
import { execSync } from 'node:child_process'
const version = JSON.parse(readFileSync('./package.json')).version
const name = assetName(version, process.platform, process.arch)
const dest = cachePath(version, name)
execSync('go build -o /tmp/ca-mcp ../cmd/codebase-analyser-mcp')
mkdirSync(dirname(dest), { recursive: true })
copyFileSync('/tmp/ca-mcp', dest)
console.log('seeded cache at', dest)
"
node index.js < /dev/null
```
Expected: the second command prints nothing about downloading, starts the binary, reads EOF on stdin, and exits. A "downloading" line on stderr means the cache-hit path is not being taken.

- [ ] **Step 7: Write `npm/README.md`**

A short page: what the package is, the three host config snippets in `npx -y @codebase-analyser/mcp` form, the supported platforms table, and one line saying the binary is cached under the OS cache dir and verified against the release checksums before it runs.

- [ ] **Step 8: Update `docs/mcp-server.md`**

In all three config snippets, replace

```json
"command": "codebase-analyser-mcp"
```

with

```json
"command": "npx",
"args": ["-y", "@codebase-analyser/mcp"]
```

(and the TOML equivalent: `command = "npx"` / `args = ["-y", "@codebase-analyser/mcp"]`). Keep the local-binary form below it under a "Using a locally built binary" heading — it is what contributors and the e2e test use.

- [ ] **Step 9: Report to the reviewer**

Publishing to npm and pushing a release tag are the user's calls; do neither. State that the download path is unverified until a real release exists, and that `OWNER` in `package.json` and `index.js` still needs the real GitHub owner. Then emit one single-line commit message and stop.

---

## Pending cross-session follow-up

**Error string, once JS/TS detection lands.** `internal/mcpserver/analyze.go` returns `"no Go or Rust project found under %s"` and `mcp_e2e_test.go` asserts that substring. A concurrent session is adding `package.json` detection; when their work lands, `detect.Detect` will find JS/TS projects and this message becomes wrong. Change both to:

```
no analysable project found under %s (looked for go.mod, Cargo.toml, package.json)
```

Do this **after** their detection change is committed, not before — changing it early makes the message wrong in the other direction, claiming we look for `package.json` when `detect.Detect` still does not.

**Task 9's download primitives are shared infrastructure.** `download` (fetch + sha256 verify), `extractTarGz`, `safeJoin` and `lock` are unexported but live in `internal/toolchain`, so a future `node.go` in the same package can use them directly with no export needed. Node has no `GOTOOLCHAIN`/`RUSTUP_TOOLCHAIN` equivalent — no environment variable repoints `node` at another version — so a `toolchain.Node` honouring `.nvmrc`/`engines.node` genuinely needs a real fetch-verify-unpack, which is exactly what Task 9 builds. Write those four helpers to be language-agnostic (they already are); do not fold Go-specific assumptions into them.

## Post-plan notes

**What is deliberately not here** (spec: Future work):
- Background-run + status-poll tool pair. Calls block; if hosts actually time out in practice, that is the fix, and it is not built speculatively.
- `ToolAdapter`/`Resolver` implementations for Java, Python, JS/TS, React, Next.js. The interfaces are shaped for them; the implementations are a later plan.
- A Claude Code-native Skill wrapper over this server.

**Deviations from the spec, and why:**
- Cache keyed by tool-binary identity (path, size, mtime) rather than a parsed version string — same invalidation guarantee, no `Version()` method on `ToolAdapter` and its five implementations.
- Stage 3 reaches for `GOTOOLCHAIN` and `RUSTUP_TOOLCHAIN` before writing a downloader. Both languages ship exactly this mechanism; Task 9 adds the download path only for the machine that has neither language installed, which is the part those variables genuinely cannot cover.
- `Resolver.Ensure` returns `([]string, error)` — the environment that selects the toolchain — rather than the spec's `(toolchainPath string, err error)`. A path alone cannot express `GOTOOLCHAIN`/`RUSTUP_TOOLCHAIN` selection, and the caller's only use for the value is to hand it to a subprocess. `EnsureGo` in Task 9 still returns a real GOROOT path for the download case.
- Task 6 adds `orchestrator.RunWithCache` and an `orchestrator.Cache` interface rather than caching inside `internal/cache` alone: the fan-out, install memoization and panic recovery all live in the orchestrator, and duplicating them elsewhere to cache around them would be the larger diff.
- Stage 2 opens with a measurement and an explicit instruction to stop if the warm re-run is already fast. golangci-lint and clippy cache per package internally, so the cache may be re-solving a solved problem on this codebase.
