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
      "command": "npx",
      "args": ["-y", "@codebase-analyser/mcp"],
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
command = "npx"
args = ["-y", "@codebase-analyser/mcp"]

[mcp_servers.codebase-analyser.env]
DASHBOARD_URL = "https://dashboard.example.com"
DASHBOARD_TOKEN = "your-repo-ingest-token"
```

### Gemini CLI — `~/.gemini/settings.json`

```json
{
  "mcpServers": {
    "codebase-analyser": {
      "command": "npx",
      "args": ["-y", "@codebase-analyser/mcp"],
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

## Using a locally built binary

If you are contributing or testing unreleased code, build the binary yourself:

```bash
go build -o codebase-analyser-mcp ./cmd/codebase-analyser-mcp
```

Then use it directly in your MCP configuration (replace `npx` and `args` with `command`):

**Claude Code:**
```json
{
  "mcpServers": {
    "codebase-analyser": {
      "command": "./codebase-analyser-mcp",
      "env": {
        "DASHBOARD_URL": "https://dashboard.example.com",
        "DASHBOARD_TOKEN": "your-repo-ingest-token"
      }
    }
  }
}
```

**Codex CLI:**
```toml
[mcp_servers.codebase-analyser]
command = "./codebase-analyser-mcp"

[mcp_servers.codebase-analyser.env]
DASHBOARD_URL = "https://dashboard.example.com"
DASHBOARD_TOKEN = "your-repo-ingest-token"
```

**Gemini CLI:**
```json
{
  "mcpServers": {
    "codebase-analyser": {
      "command": "./codebase-analyser-mcp",
      "env": {
        "DASHBOARD_URL": "https://dashboard.example.com",
        "DASHBOARD_TOKEN": "your-repo-ingest-token"
      }
    }
  }
}
```
