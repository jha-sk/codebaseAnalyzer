# Codebase Analyser — CLI & Core Engine Design

**Date:** 2026-08-13
**Status:** Approved (v1 scope)

## Purpose

A CLI tool that analyses Go and Rust codebases for production-safety issues and reports them as clear "do's and don'ts," so developers catch correctness, concurrency, security, and operational-readiness problems before they hit production.

This spec covers the **CLI and core analysis engine only**. CI integration (PR comments, build gating) and a web dashboard (historical trends, multi-repo/team views) are separate future products, deliberately out of scope here — each needs its own design once this engine's real output shape is known.

## Non-goals (v1)

- CI/CD integration (GitHub Actions, PR comments, build gating) — future spec.
- Web dashboard / historical trend tracking / multi-tenancy — future spec.
- Support for languages other than Go and Rust.
- Custom analysis engine / own parser — v1 wraps existing tools only.
- Dynamic analysis (e.g. `go test -race`) — v1 is static analysis only.
- Project-level config file — CLI flags only for v1.

## Implementation language

**Go.** The workload is process orchestration (running linters concurrently), JSON parsing, and HTTP calls to an LLM API — not custom parsing or CPU-bound work. Go's goroutines, mature CLI ecosystem (cobra), and trivial static-binary cross-compilation fit this directly; this is the same pattern `golangci-lint` already proves at scale. Rust would add ceremony without a payoff here.

## Architecture

Single binary, `analyser`, structured as a linear pipeline:

```
detect project(s) → run wrapped tools (parallel) → normalize findings → batch-explain via LLM → render report → exit code
```

1. **Detect**: walk the target path for `go.mod` / `Cargo.toml`. A single repo may contain both (e.g. a Go service with a Rust sidecar) — both get analyzed.
2. **Run tools**: for each detected language, run its wrapped tools concurrently via goroutines. Each tool is wrapped by a small adapter implementing:
   ```go
   type ToolAdapter interface {
       Name() string
       CheckInstalled() bool
       Install() error
       Run(path string) ([]Finding, error)
   }
   ```
3. **Normalize**: each adapter maps its tool's native JSON output into a common struct:
   ```go
   type Finding struct {
       File, Tool, RuleID, Category, Severity, Message string
       Line int
   }
   ```
   `Category` is one of `correctness | concurrency | security | operational`. `Severity` is normalized to `critical|high|medium|low` via a per-tool mapping table (each tool's native severity/confidence levels are mapped once, in the adapter).
4. **Explain**: findings are grouped by `(tool, ruleID)`. One batched LLM prompt per group requests a short "why this matters in production" explanation plus a general fix pattern, reused across all instances of that rule in this run (not one call per finding).
5. **Render**: human-readable terminal report by default, or `--format json`.
6. **Exit code**: non-zero if any finding is at/above `--severity` threshold (default: `high`).

Each stage is independently testable: adapters are tested against captured sample tool output, normalization/severity-mapping are pure functions, and the LLM step is mockable behind its own interface.

## Tools wrapped

| Language | Tool | Category coverage |
|---|---|---|
| Go | `golangci-lint` | Correctness (govet, errcheck, staticcheck), concurrency misuse (staticcheck SA lints), operational (via `contextcheck`, `bodyclose`, `noctx` linters enabled explicitly) |
| Go | `gosec` | Security (hardcoded secrets, unsafe crypto, injection risks, unsafe file perms) |
| Go | `govulncheck` | Security — known CVEs in dependencies, call-graph aware (only flags reachable vulnerabilities) |
| Rust | `clippy` | Correctness, concurrency misuse (lock/mutex lints) |
| Rust | `cargo-audit` | Security — known CVEs in `Cargo.lock` |

**Operational/production-readiness** has no single off-the-shelf tool for either language; v1 covers what `golangci-lint`'s explicitly-enabled linters catch (missing context propagation, unclosed response bodies, missing timeouts on HTTP calls). Deeper operational checks (e.g. project-specific "this retry loop has no backoff") are a known gap, deferred to a future custom-detector layer — not silently assumed as covered.

**Concurrency scope note**: true data-race detection (`go test -race`) is dynamic — it requires running tests, a fundamentally different mode than static source scanning. V1 sticks to static misuse lints (e.g. copying a `sync.Mutex`, lock/unlock imbalance). A `--race` flag to additionally shell out to `go test -race` is a plausible future addition, not in v1.

## LLM explanation

- **Provider selection**: check env vars in priority order — `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` — use whichever is set first. `--llm-provider` flag overrides. If none are set, the report still runs with raw findings only, plus a note on how to enable explanations.
- **Batching**: one prompt per `(tool, ruleID)` group, listing all instances (file:line) found in this run, asking for a short explanation + general fix pattern. The response is reused across all findings in that group for this run — not cached across runs or persisted to disk in v1.
- **Failure handling**: if the LLM call fails (rate limit, network, bad key), that group's findings fall back to raw tool output with no explanation. Never blocks the rest of the report.

## Report output

**Human-readable (default)**: grouped by category → severity → rule, showing all file:line locations under each rule plus its shared explanation. Summary line at the top with counts by severity.

**JSON (`--format json`)**: flat list, explanation duplicated per finding (keeps downstream/future CI tooling simple — no need to understand the grouping):
```json
{
  "summary": { "critical": 0, "high": 3, "medium": 12, "low": 5 },
  "findings": [
    { "file": "...", "line": 42, "tool": "gosec", "ruleID": "G104",
      "category": "correctness", "severity": "high",
      "message": "...", "explanation": "..." }
  ]
}
```

## CLI interface

```
analyser run <path> [flags]

Flags:
  --format string      human | json (default "human")
  --severity string    minimum severity to fail on: critical|high|medium|low (default "high")
  --category strings   restrict to categories (default: all)
  --llm-provider string  override provider auto-detection
  --no-llm              skip explanations entirely (raw findings only)
```

No project-level config file in v1 — flags and defaults are enough to start; a config file is a candidate addition once flag usage patterns are clear.

## Error handling

- **Missing tool**: auto-install once at start (`go install .../gosec@latest`, `cargo install cargo-audit`, etc.), with a visible "installing X..." message. If install fails (no network/toolchain), that tool is skipped and clearly noted in the report summary — never fails the whole run.
- **Tool crash or timeout**: recorded as skipped with its error; a per-tool timeout (default 5 min) prevents one hung tool from blocking the run.
- **LLM failure**: falls back to unexplained findings for that group (see above).
- **No project detected** (`go.mod`/`Cargo.toml` not found): clear error, non-zero exit, no silent empty report.

## Testing strategy

- **Tool adapters**: unit tests against captured real sample JSON output per tool → assert correct `Finding` mapping.
- **Normalizer / severity mapping**: pure-function unit tests.
- **LLM step**: interface-mocked; tests cover batching/grouping logic, not live API calls.
- **End-to-end smoke test**: fixture Go repo + fixture Rust repo, each with a couple of known-bad patterns (e.g. ignored error, hardcoded secret), run the full CLI, assert expected findings appear in the report. Validates the whole pipeline works together.

## Future work (explicitly deferred)

- CI integration (PR comments, build gating) — separate spec, builds on the `--format json` output.
- Web dashboard with historical trends, multi-repo/team views — separate spec, needs its own storage/auth/multi-tenancy design.
- Custom detector layer for operational-readiness gaps existing tools don't cover.
- `--race` flag for dynamic data-race detection via `go test -race`.
- Project-level config file, if flag-based configuration proves unwieldy.
- Support for languages beyond Go and Rust.
