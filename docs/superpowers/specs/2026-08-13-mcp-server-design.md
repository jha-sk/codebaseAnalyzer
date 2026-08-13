# Codebase Analyser — MCP Server Design

**Date:** 2026-08-13
**Status:** Approved (v1 scope)

## Purpose

A distribution layer that lets any MCP-compatible coding agent (Claude Code, Codex CLI, Gemini CLI, and others) use the analyser directly — no `git clone`, no separate install step. The agent's MCP config points at one command; the agent gets an `analyze_codebase` tool it can call like any other.

This is packaging on top of the engine already designed in `2026-08-13-codebase-analyser-cli-design.md`, not a new engine. It does not replace the CLI (still the right interface for CI) or the dashboard (still the right interface for persistent history) — it's a third front door onto the same core.

## Why MCP, not a Claude Code Skill

Claude Code's Skill system (the mechanism used throughout this project's own design process) is Anthropic-specific and doesn't work in Codex or Gemini CLI. MCP is the protocol Claude Code, Codex CLI, and Gemini CLI all support, so it's the only path that reaches all three with one implementation.

## Architecture

- New binary, `codebase-analyser-mcp`, imports the same core engine package the CLI uses (detect → tools → normalize → report). No reimplementation — a different front door on the existing pipeline.
- Implements MCP over stdio using an existing Go MCP SDK (not hand-rolled JSON-RPC) — the standard transport for locally-spawned MCP servers.
- Distributed via a thin npm wrapper package. `npx -y @org/codebase-analyser-mcp` is the one line an agent's MCP config needs; on invocation the wrapper detects OS/arch, downloads the matching prebuilt Go binary from GitHub Releases (cached after first run), and execs it, forwarding stdio through. Same pattern tools like esbuild and biome use to ship a compiled binary through npm.
- Reuses the CLI's existing missing-tool handling (auto-install via `go install`/`cargo install`, skip-and-report on failure) unchanged.

## Tools exposed

1. **`analyze_codebase(path?, category?, severity?)`** — runs the pipeline, returns findings + summary + tool-run status (which linters ran/were skipped). `path` defaults to the current working directory if omitted.
   - **No LLM explanation field.** The CLI calls an LLM itself because it's a standalone tool with nothing else in the loop; the MCP server's caller is *already* an LLM agent, so it reads raw findings and explains/prioritizes them itself. This removes an entire dependency (LLM provider config) from this path.
   - **Response is capped**: full detail for the top ~50 findings by severity, plus accurate total counts by severity/category for everything else, with an explicit "N more not shown" note. The return value becomes tokens directly in the calling agent's context (unlike the CLI, which writes to a terminal or file), so an uncapped response on a large repo would be expensive and could crowd out the agent's other context. The agent can narrow further via the existing `category`/`severity` filter params.
2. **`push_to_dashboard()`** — pushes the most recent analysis result to a dashboard. `DASHBOARD_URL` and `DASHBOARD_TOKEN` are configured once as env vars in the MCP server's config block (same pattern as the CLI's `--dashboard-url` flag) — the token never enters the LLM's context or any transcript.

## Reliability

- Tool calls block until the analysis completes — no background-job/poll split in v1. First-run latency (toolchain download + tool install + lint pass) can be several minutes; this is documented explicitly rather than hidden. The pipeline's existing 5-minute per-tool timeout remains the ceiling.
- If real usage shows hosts actually timing out on long calls, revisit with a background-run + status-poll tool pair — not built speculatively now.

## Performance — incremental analysis cache

- Repeated calls within an agent's fix-and-recheck loop skip work that hasn't changed, rather than re-running everything fresh every time.
- **Invalidation granularity is package/crate, not individual file.** These tools (golangci-lint, clippy, govulncheck) reason at the package/crate level — a change in one file can affect diagnostics reported against another file in the same package via type-checking. File-level invalidation would risk silently stale findings; package/crate-level is the safe unit.
- Cache persists to disk (`~/.cache/codebase-analyser/...`), keyed by repo path **and** tool version, so upgrading a wrapped linter invalidates stale entries automatically instead of silently serving output from an old tool version.

## Compatibility

- Prebuilt binaries for macOS (arm64, amd64), Linux (arm64, amd64), Windows (amd64) — 5 targets, standard Go cross-compilation as part of the release pipeline.
- **No system Go/Rust toolchain required.** The analyser downloads and manages its own isolated toolchains — never touching the user's system install — the same approach `rustup`/`nvm` use. This needs only network access (already required for the `npx` fetch step), not a container runtime.
- The toolchain fetched matches what the target repo declares (`go.mod`'s `go` directive, `rust-toolchain.toml`), falling back to latest stable if undeclared — running the wrong language version risks false positives/negatives (flagging valid syntax, missing a real deprecation).
- **Toolchain resolution is a pluggable per-language interface**, not two hardcoded functions:
  ```
  type ToolchainResolver interface {
      Detect(repoPath string) (version string, ok bool)  // read go.mod / rust-toolchain.toml / etc.
      Ensure(version string) (toolchainPath string, err error)  // download+cache if not already present
  }
  ```
  Go and Rust are the only two implementations shipped in v1. This exists specifically because Java, Python, JS/TS, React, and Next.js support are on the roadmap — a resolver interface means adding those later is a new implementation of an existing shape, not a rework of the toolchain manager itself.

## Multi-language extensibility

Nothing else in this design is Go/Rust-specific in a way that would need rework for future languages:
- The `ToolAdapter` interface and common `Finding` struct (from the CLI spec) were already generic — a Python adapter (ruff/bandit/pip-audit) or JS/TS adapter (eslint/npm-audit) plugs into the same pipeline shape as golangci-lint/clippy do today.
- Categories (Correctness/Concurrency/Security/Operational) are language-agnostic concepts; only the tool adapters underneath differ per language.
- Repo detection (`go.mod`/`Cargo.toml`) extends the same way to `package.json`/`pyproject.toml`/`pom.xml`.

The toolchain resolver interface above closes the one gap that would otherwise have needed rework.

## Testing strategy

- Toolchain resolver: unit tests per language against fixture `go.mod`/`rust-toolchain.toml` files, asserting correct version detection and fallback-to-latest behavior.
- Incremental cache: explicit test that an unchanged package/crate is skipped on a second call, and that a changed file invalidates its whole package/crate (not just itself).
- Response capping: test that a fixture repo with >50 findings returns exactly the top 50 plus accurate summary counts and a "more not shown" note.
- MCP protocol conformance: a smoke test exercising the actual stdio JSON-RPC exchange (tool discovery, `analyze_codebase` call, `push_to_dashboard` call) against the Go MCP SDK's test harness.
- npm wrapper: test platform-detection logic and binary-download/cache path across the 5 target platforms (can be matrix-run in CI without needing real hardware for each).

## Future work (explicitly deferred)

- Background-run + status-poll tools, if blocking calls prove to time out in real host environments.
- Additional `ToolAdapter` and `ToolchainResolver` implementations for Java, Python, JS/TS, React, Next.js.
- Claude Code-native Skill wrapper for a more polished in-Claude-Code experience, layered on top of the same MCP server (deferred — MCP alone was chosen as the v1 distribution target).
