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
