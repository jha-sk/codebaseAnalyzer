# Codebase Analyser — Dashboard Design

**Date:** 2026-08-13
**Status:** Approved (v1 scope; visual direction confirmed, treated as directional — see UI/UX note)

## Purpose

A self-hosted web dashboard that ingests CLI run results and gives a team a persistent, cross-run view of code health: trends over time, category/severity breakdown, per-branch comparison, and full drill-down into any run's findings. It's the persistent counterpart to the CLI's one-shot report.

Builds on the CLI & core engine (`2026-08-13-codebase-analyser-cli-design.md`). Out of scope for v1: regression alerting, data retention/pruning, languages beyond Go/Rust, user accounts/RBAC.

## Architecture

- Single Go binary: API + an embedded, pre-built SPA frontend (via `go:embed`), backed by Postgres.
- Ships as a Docker image + `docker-compose.yml` (app + Postgres) — self-hosted, one command to stand up.
- The CLI gains a `--dashboard-url` flag plus a repo ingest token; on completion it POSTs its existing JSON report to the dashboard, with repo/branch/commit metadata auto-detected via local `git` (remote URL, current branch, HEAD commit).
- The push is best-effort: if the dashboard is unreachable, the CLI prints a warning and continues — a dashboard outage never fails a CI run or overrides the CLI's own severity-based exit code.
- Re-pushing the same commit (e.g. a CI retry) overwrites that commit's stored run rather than creating a duplicate entry.
- All branches ingest and are viewable, not just the default branch; the UI defaults to the repo's default branch with a switcher for others.
- Retention is unlimited in v1 — no pruning job. Findings volume per run is small; a self-hosted team can manage its own DB size if this ever becomes an issue.

## Auth

Two token types, no user accounts:
- **Admin token** — set once as an env var at deploy time. Gates the dashboard UI and all repo-management actions.
- **Per-repo ingest token** — issued when an admin registers a repo (identified by its normalized git remote URL, stripped of `.git` suffix / protocol differences). Shown once at registration; that repo's CI uses it to push runs. A leaked ingest token can only push fake data for its own repo, not others.

## Data model

- `repos`: id, remote_url (normalized, unique), ingest_token_hash, registered_at
- `runs`: id, repo_id (FK), branch, commit_sha, pushed_at, severity summary counts — unique on `(repo_id, commit_sha)`, upserted so retries overwrite rather than duplicate
- `findings`: id, run_id (FK), file, line, tool, rule_id, category, severity, message, explanation

## API

- `POST /api/admin/repos` (admin token) — register a repo by remote URL → returns its ingest token (shown once)
- `GET /api/admin/repos` (admin token) — list repos; regenerate or revoke a repo's token
- `POST /api/repos/:repo/runs` (that repo's ingest token) — push a run: the CLI's JSON report plus branch/commit metadata
- `GET /api/repos/:repo/runs?branch=` (admin token) — run history for a branch, used by the branches table and trend chart
- `GET /api/runs/:id` (admin token) — full findings for one run, used for drill-down

## UI / UX design

The dashboard is one page per repo: a dark glass-panel layout (a softly animated gradient background behind translucent, blurred cards) built from eight functional views, stacked top to bottom. **The specific visual treatment below (exact colors, motion curves, component boundaries) was validated through interactive mockups during brainstorming and is directional, not a pixel spec — implementation may adjust it once built against real data and an actual component library instead of hand-rolled SVG/CSS.** What should carry through unchanged is the *set of views* and what each one is for.

1. **Top bar** — brand, repo/branch switcher, live status indicator.
2. **All branches** — every branch that has pushed a run, ranked by recency: name, last-run time, commit, a compact stacked severity bar (composition at a glance, not just raw numbers), and a color-coded health-score indicator. Selecting a row switches the rest of the page to that branch. This is the page's entry point for cross-branch comparison.
3. **Severity summary cards** (Critical/High/Medium/Low) — current count, delta vs. the previous run, a small embedded trend sparkline. Clicking a card filters the findings table at the bottom to that severity.
4. **Findings-over-time chart** — the core trend view: hovering shows an exact per-severity readout at that point in time; legend entries toggle a series on/off; the busiest point is called out directly on the chart.
5. **Health score** — a single weighted 0–100 score (severity-weighted, trend-adjusted) as a fast at-a-glance signal alongside the detailed chart, for someone who wants one number.
6. **Findings by category** — a breakdown over the four categories from the CLI spec (Correctness / Concurrency / Security / Operational). This is information the severity-only cards can't show: *where* the risk concentrates, not just how bad it is.
7. **Since last run** — new vs. fixed finding counts between the two most recent runs on the branch. The most directly motivating number for a developer checking in day to day.
8. **Tool run status** — one row per wrapped tool (golangci-lint, gosec, govulncheck, clippy, cargo-audit) showing whether it ran or was skipped (e.g. install failed). Makes the CLI's error-handling behavior (skip-and-continue on a missing tool) visible here instead of silently absent from the numbers.
9. **Top offending files** — files ranked by finding count: a quick "where do I start" list.
10. **Recent activity feed** — the last several runs with relative time and a new/fixed delta, independent of the chart — useful for scanning history without reading axis labels.
11. **Analysis pipeline diagram** — a live visualization of the CLI's actual pipeline (Go/Rust repo → per-language tools → normalize → LLM-explain → report). A skipped tool renders as visually disconnected (dashed, no data flowing) rather than just missing. This view is a "the system is alive and wired correctly" signal more than a primary data view — lowest priority of the eleven if something has to be cut to ship v1 sooner.
12. **Current-run findings table** — the full list for the selected run, each row expandable inline to show the LLM-generated explanation (why it matters + suggested fix) from the CLI's report. This is the dashboard's equivalent of the CLI's human-readable output, made browsable instead of scrolled — and it stays alongside the branches/trend views rather than replacing them, since comparing "what's true now" against "how did we get here" is the point of having both.

**Motion is deliberate, not decorative**: cards and rows enter staggered on load; numbers count up with an ease-out curve rather than a linear tick; the trend line draws itself in; hover interactions glide to the nearest data point rather than snapping; a findings row expands via a smooth accordion rather than an instant toggle. Two consistent easing curves are used throughout (a smooth decelerate for data/motion, a slight spring/overshoot for hover feedback) rather than ad hoc per-element choices — the goal is a page that feels responsive without being distracting.

## Testing strategy

- API handlers: unit tests per endpoint (register, ingest, list, get) against a real test Postgres instance.
- Upsert-by-commit behavior: an explicit test asserting a re-push of the same `(repo_id, commit_sha)` overwrites rather than duplicates.
- Frontend: component-level tests for the interactive pieces (severity filter, row expand, chart hover) once a concrete component library is chosen at implementation time.
- End-to-end smoke test: push two runs for a fixture repo through the real ingest API, assert the branches and run-history endpoints reflect both correctly.

## Future work (explicitly deferred)

- Regression alerting (Slack/webhook on a severity increase between runs).
- Retention/pruning policy, if data volume becomes a real operational concern.
- Per-user accounts/RBAC, if a dashboard instance needs to be exposed beyond a trusted network.
- Cross-repo leaderboard view (deferred in v1 in favor of the per-repo trend as the primary view; branches table partially covers cross-branch comparison within one repo).
