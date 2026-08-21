# codebase-analyser

Production-safety static analysis for **Go**, **Rust**, **JavaScript**, **TypeScript** and **Python** — as a CLI, as an MCP tool for coding agents, and as a self-hosted dashboard for tracking a repo's health over time.

It does not invent its own linters. It detects every project in a tree, runs the ecosystem-standard tools against each one, normalises their output into a single severity/category model, caches per package so a fix-and-recheck loop only re-lints what changed, and (optionally) has an LLM explain each finding class.

```
repo ──▶ detect ──▶ run tools ──▶ normalise ──▶ cache ──▶ report ──┬──▶ terminal / JSON
        (go.mod,     (per project,   (severity +            │        ├──▶ MCP tool result
         Cargo.toml,   concurrent)    category)             │        └──▶ dashboard
         package.json)                                      └── per-package fingerprints
```

| | |
|---|---|
| **Languages** | Go, Rust, JavaScript, TypeScript, Python |
| **Categories** | `correctness` · `concurrency` · `security` · `operational` |
| **Severities** | `critical` · `high` · `medium` · `low` |
| **Surfaces** | `analyser` CLI · `codebase-analyser-mcp` MCP server · `dashboard` web app |

---

## Contents

1. [What runs under the hood](#1-what-runs-under-the-hood)
2. [Prerequisites](#2-prerequisites)
3. [Build](#3-build)
4. [Use case A — run it on a codebase (CLI)](#4-use-case-a--run-it-on-a-codebase-cli)
5. [Use case B — add it as an MCP server](#5-use-case-b--add-it-as-an-mcp-server)
6. [Use case C — run and view the dashboard](#6-use-case-c--run-and-view-the-dashboard)
7. [Use case D — wire it into CI](#7-use-case-d--wire-it-into-ci)
8. [Configuration reference](#8-configuration-reference)
9. [Developing](#9-developing)

---

## 1. What runs under the hood

| Language | Detected by | Tools run |
|---|---|---|
| Go | `go.mod` | `golangci-lint`, `gosec`, `govulncheck` |
| Rust | `Cargo.toml` | `clippy`, `cargo-audit` |
| JavaScript | `package.json` | `eslint`, dependency audit (`npm`/`yarn`/`pnpm`) |
| TypeScript | `package.json` + `tsconfig.json` | `eslint`, `tsc`, dependency audit |
| Python | `pyproject.toml` / `requirements.txt` / `setup.py` | `ruff`, `mypy`, dependency audit (`pip-audit`) |

A single repo can hold many projects, and one directory can be more than one project (a Go service with a JS build pipeline is both). Every one of them is analysed, concurrently. `.git`, `node_modules`, `vendor`, `target`, `testdata`, `dist`, `build`, `.next`, `out`, `coverage`, `.venv`, `venv`, `__pycache__`, `.mypy_cache`, `.ruff_cache` and `*.egg-info` are never descended into.

**Missing tools are installed on demand.** Go and Rust toolchains are downloaded and verified into the analyser's own cache directory; JS tooling is `npm install`ed into a private, pinned tool directory — never into your repo's `node_modules`, never globally. This is why the first run on a fresh machine can take several minutes and later runs are fast.

**Caching is per package/crate, not per file.** These tools reason at the package level — a change in one file can alter a diagnostic reported against a different file in the same package — so file-level invalidation would serve stale results. Cache and downloaded toolchains live under `$XDG_CACHE_HOME/codebase-analyser` (or the OS cache dir), overridable with `CODEBASE_ANALYSER_CACHE`.

---

## 2. Prerequisites

| Requirement | Needed for | Auto-installed? |
|---|---|---|
| Go 1.26+ | building this repo | no |
| Go toolchain | analysing Go projects | ✅ downloaded on demand |
| Rust toolchain | analysing Rust projects | ✅ via rustup on demand |
| Python 3 / pip | analysing Python projects | ✅ ruff/mypy/pip-audit installed into a private venv on demand |
| **Node.js 20+ / npm** | analysing JS/TS projects | ❌ **must be on `PATH`** |
| Docker + Compose | the dashboard | no |
| PostgreSQL | the dashboard | ✅ via Compose |

An LLM API key is optional — see [explanations](#explanations-optional).

---

## 3. Build

```bash
git clone https://github.com/jha-sk/codebaseAnalyzer.git
cd codebaseAnalyzer

go build -o bin/analyser                 ./cmd/analyser
go build -o bin/codebase-analyser-mcp    ./cmd/codebase-analyser-mcp
go build -o bin/dashboard                ./cmd/dashboard     # or use Docker, see §6
```

Optionally put `bin/analyser` on your `PATH`:

```bash
sudo install -m755 bin/analyser /usr/local/bin/analyser
```

Verify:

```bash
./bin/analyser run --help
```

---

## 4. Use case A — run it on a codebase (CLI)

### 4.1 The basic run

```bash
analyser run .
```

That detects every project under `.`, installs anything missing, runs all tools concurrently, and prints a human report grouped by severity. Point it anywhere: `analyser run ~/work/some-service`.

### 4.2 Reading the exit code

This is the part CI cares about, and it is deliberately three-valued:

| Code | Meaning |
|---|---|
| `0` | Clean pass: nothing at or above `--severity`, **and** every tool ran |
| `1` | At least one finding at or above `--severity` |
| `2` | Nothing at or above `--severity`, **but coverage was incomplete** — a tool was skipped, crashed, timed out, or couldn't install |

Exit `2` exists so a repo is never reported clean just because the analysis silently didn't happen. The report names which tool was skipped and why.

### 4.3 Narrowing the run

```bash
# Only fail on critical issues
analyser run . --severity critical

# Only look at security and concurrency
analyser run . --category security,concurrency

# Both
analyser run . --severity medium --category correctness
```

`--severity` sets the *failure threshold*, not a display filter — lower-severity findings still appear in the report, they just don't change the exit code. `--category` restricts what is reported at all.

### 4.4 Machine-readable output

```bash
analyser run . --format json > findings.json
```

The document carries `incomplete` and `skippedTools` alongside a `summary` and the `findings` array, so a consumer can tell "clean" from "didn't run". Each finding has `file`, `line`, `tool`, `ruleID`, `category`, `severity`, `message`, plus `explanation` and `fixPattern` when an LLM is configured.

### 4.5 Explanations (optional)

If an API key is present in the environment, each *class* of finding (tool + rule) gets a short "Why it matters" and "Fix pattern" paragraph. Providers are auto-detected in this order:

| Provider | Env var |
|---|---|
| Anthropic | `ANTHROPIC_API_KEY` |
| OpenAI | `OPENAI_API_KEY` |
| Gemini | `GEMINI_API_KEY` |

```bash
export ANTHROPIC_API_KEY=sk-ant-...
analyser run .                          # explanations on

analyser run . --llm-provider openai    # force a provider (errors if its key is unset)
analyser run . --no-llm                 # raw findings, no network calls
```

No key set is a normal state, not an error: you get raw findings. Explanations are per rule, not per occurrence, so cost stays flat as findings grow.

---

## 5. Use case B — add it as an MCP server

`codebase-analyser-mcp` speaks MCP over stdio and exposes two tools to any MCP-capable agent:

| Tool | Arguments | Returns |
|---|---|---|
| `analyze_codebase` | `path` *(optional, defaults to the server's working directory)*, `category` *(optional list)*, `severity` *(optional)* | Findings, per-severity and per-category totals, and which tools were skipped |
| `push_to_dashboard` | none | Pushes the most recent analysis to the configured dashboard |

Findings are returned **raw, with no LLM explanation** — the caller is already a model and does its own explaining.

> **Response size.** Full detail is returned for the 50 most severe findings, with accurate totals for all of them and a `note` saying how many were withheld. The result lands directly in the agent's context, so an uncapped response on a large repo would be expensive. Narrow with `category` and `severity` to see the rest.

> **Latency.** Calls block until analysis finishes. The first run against a repo can take several minutes while linters install. Each tool is capped at 5 minutes.

### 5.1 Point it at a binary

Use an **absolute path** to the binary you built in §3 — the server's working directory is what `analyze_codebase` defaults to analysing:

```
/abs/path/to/codebaseAnalyzer/bin/codebase-analyser-mcp
```

Once the npm package is published, `npx -y @codebase-analyser/mcp` works as a drop-in replacement for `command` + `args` in every config below.

The two dashboard env vars are **optional** — omit them if you only want `analyze_codebase`. `DASHBOARD_TOKEN` is read from the server's environment and is never returned to the model.

### 5.2 Claude Code

Fastest path — one command:

```bash
claude mcp add codebase-analyser \
  --env DASHBOARD_URL=http://localhost:8080 \
  --env DASHBOARD_TOKEN=<repo ingest token> \
  -- /abs/path/to/bin/codebase-analyser-mcp
```

Or edit `.mcp.json` in the project (checked in, shared with the team) or `~/.claude.json` (personal):

```json
{
  "mcpServers": {
    "codebase-analyser": {
      "command": "/abs/path/to/bin/codebase-analyser-mcp",
      "env": {
        "DASHBOARD_URL": "https://dashboard.example.com",
        "DASHBOARD_TOKEN": "your-repo-ingest-token"
      }
    }
  }
}
```

Restart Claude Code, then run `/mcp` — `codebase-analyser` should list both tools.

### 5.3 Codex CLI

Edit `~/.codex/config.toml`:

```toml
[mcp_servers.codebase-analyser]
command = "/abs/path/to/bin/codebase-analyser-mcp"

[mcp_servers.codebase-analyser.env]
DASHBOARD_URL = "https://dashboard.example.com"
DASHBOARD_TOKEN = "your-repo-ingest-token"
```

### 5.4 Gemini CLI

Edit `~/.gemini/settings.json`:

```json
{
  "mcpServers": {
    "codebase-analyser": {
      "command": "/abs/path/to/bin/codebase-analyser-mcp",
      "env": {
        "DASHBOARD_URL": "https://dashboard.example.com",
        "DASHBOARD_TOKEN": "your-repo-ingest-token"
      }
    }
  }
}
```

### 5.5 Using it

Just ask the agent in plain language — *"analyse this codebase for security issues"*, *"re-check the critical findings"*, *"push that run to the dashboard"*. The per-package cache makes the fix-and-recheck loop cheap: after the first full run, only the packages the agent actually edited are re-linted.

---

## 6. Use case C — run and view the dashboard

A self-hosted web app over every run pushed to it: trends, per-branch comparison, category breakdown, health tile, and full drill-down into any run's findings.

### 6.1 Start it

```bash
cat > .env <<'EOF'
POSTGRES_PASSWORD=<a strong password>
DASHBOARD_ADMIN_TOKEN=<a strong random token>
EOF

docker compose up -d --build
```

Open **http://localhost:8080** and sign in with `DASHBOARD_ADMIN_TOKEN`.

> ⚠️ `POSTGRES_PASSWORD` is interpolated directly into `DATABASE_URL` as a connection-string component. A password containing `@`, `/`, `#` or `?` produces a broken DSN — stick to alphanumerics, or URL-encode it yourself.

Generate tokens with `openssl rand -hex 32`.

### 6.2 Register a repo

Each repo gets its own **ingest token**, which can only write that repo's runs:

```bash
curl -sX POST localhost:8080/api/admin/repos \
  -H "Authorization: Bearer $DASHBOARD_ADMIN_TOKEN" \
  -d '{"remote_url":"git@github.com:acme/widgets.git"}'
```

The response contains the ingest token. **It is shown once** — store it as a CI secret. Lost tokens are replaced, not recovered.

| Action | Request |
|---|---|
| List repos | `GET /api/admin/repos` |
| Rotate a repo's token | `POST /api/admin/repos/<id>/token` |
| Delete a repo *(and every run and finding it owns)* | `DELETE /api/admin/repos/<id>` |

All admin routes take `Authorization: Bearer $DASHBOARD_ADMIN_TOKEN`.

### 6.3 Push a run to it

```bash
analyser run . \
  --dashboard-url http://localhost:8080 \
  --dashboard-token "$ANALYSER_DASHBOARD_TOKEN"
```

Branch and commit are read from the checkout; the remote is never read, because the dashboard identifies a repo by its ingest token, not by URL. If the dashboard is unreachable the CLI prints a warning and keeps its normal exit code — **a dashboard outage never fails a build**.

Refresh the browser and the run appears. The UI opens on `main`; every branch that pushes is stored and viewable. Re-pushing the same commit overwrites that run rather than duplicating it. Retention is unlimited — there is no pruning job.

### 6.4 Exposing it beyond localhost

**The server speaks plain HTTP by design.** The admin token (which grants access to every repo's data) and every ingest token travel as cleartext bearer headers on every request.

Do not publish `DASHBOARD_PORT` on a public interface, and do not point CI or a browser at it over an untrusted network as-is. Put a TLS-terminating reverse proxy (Caddy, nginx, a cloud load balancer) in front and expose only its HTTPS port. The Go server does not and will not terminate TLS.

---

## 7. Use case D — wire it into CI

```yaml
name: analyse
on: [push, pull_request]

jobs:
  analyse:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.26' }
      - uses: actions/setup-node@v4       # only if you analyse JS/TS
        with: { node-version: '22' }

      - name: Cache analyser toolchains and results
        uses: actions/cache@v4
        with:
          path: ~/.cache/codebase-analyser
          key: analyser-${{ runner.os }}-${{ hashFiles('**/go.sum', '**/Cargo.lock', '**/package-lock.json') }}

      - run: go build -o bin/analyser ./cmd/analyser

      - run: |
          ./bin/analyser run . \
            --severity high \
            --dashboard-url https://dashboard.internal \
            --dashboard-token "$ANALYSER_DASHBOARD_TOKEN"
        env:
          ANALYSER_DASHBOARD_TOKEN: ${{ secrets.ANALYSER_DASHBOARD_TOKEN }}
```

Caching `~/.cache/codebase-analyser` is what keeps CI runs from re-downloading toolchains every time. Treat exit `2` as a real failure — it means the analysis didn't fully happen.

---

## 8. Configuration reference

### `analyser run <path>`

| Flag | Default | Description |
|---|---|---|
| `--severity` | `high` | Minimum severity that fails the run: `critical` \| `high` \| `medium` \| `low` |
| `--category` | all | Restrict to `correctness`, `concurrency`, `security`, `operational` (comma-separated) |
| `--format` | `human` | `human` \| `json` |
| `--llm-provider` | auto | Force `anthropic` \| `openai` \| `gemini` |
| `--no-llm` | off | Skip explanations entirely |
| `--dashboard-url` | — | Push this run to a dashboard at this base URL |
| `--dashboard-token` | `$ANALYSER_DASHBOARD_TOKEN` | Ingest token for `--dashboard-url` |

### Environment variables

| Variable | Used by | Purpose |
|---|---|---|
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `GEMINI_API_KEY` | CLI | Enables explanations (first one set wins) |
| `ANALYSER_DASHBOARD_TOKEN` | CLI | Default for `--dashboard-token` |
| `CODEBASE_ANALYSER_CACHE` | CLI, MCP | Override the cache + toolchain directory |
| `DASHBOARD_URL`, `DASHBOARD_TOKEN` | MCP server | Target for `push_to_dashboard` |
| `POSTGRES_PASSWORD` | Compose | Database password (alphanumeric — see §6.1) |
| `DASHBOARD_ADMIN_TOKEN` | dashboard | Gates the UI and all repo management |
| `DASHBOARD_ADDR` | dashboard | Bind address, default `:8080` |
| `DASHBOARD_PORT` | Compose | Host port mapped to the container, default `8080` |
| `DATABASE_URL` | dashboard | Postgres DSN (set for you by Compose) |

---

## 9. Developing

```bash
go test ./...                          # everything
go test -run 'EndToEnd|MCP' .          # end-to-end only: Go, Rust, JS/TS, MCP stdio
```

### Frontend

```bash
cd internal/dashboard/web/ui
npm install
npm run dev      # Vite on :5173, proxying /api to a dashboard on :8080
npm test         # component tests
npm run build    # writes ../dist — COMMIT THE RESULT
```

`internal/dashboard/web/dist/` is a **committed build artifact**: `go build` cannot run npm, so the binary embeds whatever is checked in. Rebuild and commit it with any UI change. The Docker image rebuilds the bundle from source rather than trusting `dist/`, so the image can never ship a bundle that disagrees with its source.

### Layout

| Path | What lives there |
|---|---|
| [cmd/](cmd/) | The three binaries: `analyser`, `codebase-analyser-mcp`, `dashboard` |
| [internal/detect/](internal/detect/) | Walks a tree, finds projects and their languages |
| [internal/adapter/](internal/adapter/) | One file per external tool: install, run, parse, normalise |
| [internal/orchestrator/](internal/orchestrator/) | Runs every adapter against every project, concurrently |
| [internal/cache/](internal/cache/) | Per-package fingerprinting and result reuse |
| [internal/explain/](internal/explain/) | LLM providers and prompt handling |
| [internal/report/](internal/report/) | Human and JSON renderers |
| [internal/mcpserver/](internal/mcpserver/) | MCP tool definitions |
| [internal/dashboard/](internal/dashboard/) | HTTP API, Postgres store, embedded web UI |
| [docs/](docs/) | [MCP server](docs/mcp-server.md) · [dashboard](docs/dashboard.md) · design specs and plans |

---

## License

MIT — see [LICENSE](LICENSE).
