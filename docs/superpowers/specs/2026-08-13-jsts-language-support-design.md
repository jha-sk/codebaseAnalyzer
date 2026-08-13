# Codebase Analyser — JS/TS Language Support Design

**Date:** 2026-08-13
**Status:** Approved (v1 scope)

## Purpose

Extends the analyser to JS/TS repos — the first language added beyond the initial Go/Rust scope, and the deliberate first step toward the broader multi-language roadmap (Java, Python, React, Next.js). React and Next.js are frameworks built on JS/TS, not separate languages, so this spec is the prerequisite for both; framework-specific checks are explicitly deferred to layer on top once this lands.

This is additive to the CLI/core engine design (`2026-08-13-codebase-analyser-cli-design.md`): new `ToolAdapter` implementations and a new `ToolchainResolver` implementation (the interface already designed generically in `2026-08-13-mcp-server-design.md` specifically so this wouldn't require rework). No change to the pipeline, `Finding` schema, or categories.

## Detection

- `package.json` presence → JS/TS repo.
- `tsconfig.json` presence → TypeScript (enables the `tsc --noEmit` step; no-op otherwise).
- Lockfile format (`package-lock.json` / `yarn.lock` / `pnpm-lock.yaml`) → which audit tool runs for dependency scanning.
- Workspace config (`package.json`'s `workspaces` field, `pnpm-workspace.yaml`) → treat each member package as its own analyzable unit, same "multiple sub-projects under one repo root" pattern already used for a Go+Rust repo, applied recursively.

## Tools wrapped

| Tool | Covers | Notes |
|---|---|---|
| ESLint | Correctness, Concurrency (floating promises / missing await via `eslint-plugin-promise` + `typescript-eslint`), Operational, Security | Respects the repo's own config (legacy `.eslintrc.*` or flat `eslint.config.js` — both supported) if present; falls back to the analyser's own baseline config if the repo has none. Security-focused rules (`eslint-plugin-security` and similar) run in the same pass rather than via a separate scanner. |
| `tsc --noEmit` | Correctness (type errors) | Only runs when `tsconfig.json` is present. Catches real type errors ESLint's rule-based linting structurally can't. |
| `npm audit` / `yarn audit` / `pnpm audit` | Security — dependency CVEs | Whichever matches the detected lockfile. |

**No dedicated security scanner (e.g. Semgrep) in v1** — ESLint's security plugin rules, curated into the security category via the rule mapping below, are the security-code-pattern coverage. Revisit if that proves insufficient in practice.

## No-config repos

If a repo has no ESLint config at all, the analyser applies its own opinionated baseline (`eslint:recommended` + `typescript-eslint` recommended rules when TS is detected + relevant promise/security plugins) — matching the zero-config experience Go/Rust already have via staticcheck/clippy. The baseline is authored in **both** legacy and flat config formats so it works regardless of which ESLint version is in play. If the repo has its own config, that's respected instead — the analyser never overrides a team's chosen rules.

## Severity & category mapping

ESLint natively distinguishes only `error`/`warn` — far coarser than the analyser's four-level severity scale, and unlike gosec, ESLint has no inherent sense that a security rule matters more than a style nit. Severity and category are both assigned via a **curated per-rule lookup table** (`ruleID → {category, severity}`), the same approach as the per-tool severity mapping already planned for gosec/clippy/etc., extended here to also assign category since ESLint's plugin-based rule set spans multiple categories in one tool run (unlike Go, where golangci-lint and gosec are already separated by concern).

## Toolchain

Node.js is resolved via a new implementation of the `ToolchainResolver` interface (from the MCP server design): reads `package.json`'s `engines` field or `.nvmrc`, falls back to latest LTS if undeclared, downloads/caches an isolated Node install — never touching the system Node, same as the Go/Rust resolvers.

**Tool installation is persistent and version-pinned**, not ephemeral `npx`-per-run: ESLint, its plugins, and TypeScript are installed once into a private cache directory (alongside the Node toolchain itself) and reused across runs — consistent with how the Go/Rust tools and the incremental analysis cache already work. Faster repeat runs, and pinned versions mean stable, reproducible results instead of silently shifting if a tool publishes a new version between runs.

## Excluded paths

`node_modules`, `dist`, `build`, `.next`, and other standard generated/vendored output are excluded from scanning by default — otherwise a typical JS/TS repo's dependency tree would dominate both scan time and noise.

## Error handling

Reuses the CLI's existing behavior unchanged: a failing tool (e.g. a malformed `tsconfig.json` that breaks `tsc`) is recorded as skipped with its error and reported clearly in the summary, never crashes the whole run.

## Testing strategy

Mirrors the CLI/MCP specs' approach:
- Each adapter (ESLint, tsc, npm/yarn/pnpm audit) unit-tested against captured real sample output.
- Rule→category/severity table: unit tests asserting known rule IDs map to the expected category/severity.
- Workspace detection: fixture monorepo (multiple `package.json`s under one root) asserting each member package is analyzed as its own unit.
- Dual ESLint config format: fixture repos on legacy config, flat config, and no config, asserting the right one is respected/applied in each case.
- End-to-end smoke test: fixture JS repo and fixture TS repo, each with a couple of known-bad patterns (a floating promise, an unhandled type error, a vulnerable dependency), asserting expected findings appear.

## Future work (explicitly deferred)

- React/Next.js framework-specific checks (`eslint-plugin-react-hooks`, `eslint-plugin-jsx-a11y`, Next.js-specific operational patterns), layered on top of this base JS/TS support.
- Additional package manager support (bun's `bun.lockb`) if it sees real adoption in scanned repos.
- A dedicated security scanner (e.g. Semgrep), if ESLint's plugin-based security coverage proves insufficient in practice.
- Java and Python `ToolAdapter`/`ToolchainResolver` implementations, following this same pattern.
