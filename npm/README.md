# @codebase-analyser/mcp

MCP server for codebase-analyser: static analysis for Go and Rust as an agent tool.

## Installation & Usage

```bash
npx -y @codebase-analyser/mcp
```

This is an MCP server intended to be called by MCP-compatible agents (Claude Code, Codex CLI, Gemini CLI). Configure it in your agent's MCP settings.

### Supported Platforms

| OS | Architectures |
|---|---|
| macOS (darwin) | arm64, amd64 |
| Linux | arm64, amd64 |
| Windows | amd64 |

To run on an unsupported platform, build from source:

```bash
go build ./cmd/codebase-analyser-mcp
```

## How It Works

On first run, this wrapper downloads the prebuilt MCP server binary for your platform from the GitHub release, verifies it against a signed checksums file, caches it, and executes it. Every subsequent run reuses the cached binary — it is a cache hit.

The binary is cached under your OS cache directory:
- **macOS**: `~/Library/Caches/codebase-analyser/bin/{version}/`
- **Linux**: `~/.cache/codebase-analyser/bin/{version}/`
- **Windows**: `%LOCALAPPDATA%\codebase-analyser\bin\{version}\`

Each version is cached separately, so upgrading the package does not reuse the old binary.

## Agent Configuration

### Claude Code

Add to `.claude/settings.json`:

```json
{
  "mcpServers": {
    "codebase-analyser": {
      "command": "npx",
      "args": ["-y", "@codebase-analyser/mcp"]
    }
  }
}
```

### Codex CLI

Add to your Codex config:

```toml
[[mcpServers]]
name = "codebase-analyser"
command = "npx"
args = ["-y", "@codebase-analyser/mcp"]
```

### Gemini CLI (Google)

Add to your Gemini config:

```toml
[[mcpServers]]
name = "codebase-analyser"
command = "npx"
args = ["-y", "@codebase-analyser/mcp"]
```

### Using a locally built binary

If you are contributing or testing against unreleased code, build the binary yourself:

```bash
go build -o /tmp/codebase-analyser-mcp ./cmd/codebase-analyser-mcp
```

Then configure it directly:

```json
{
  "mcpServers": {
    "codebase-analyser": {
      "command": "/tmp/codebase-analyser-mcp"
    }
  }
}
```

## License

MIT
