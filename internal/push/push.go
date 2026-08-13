// Package push sends a completed CLI run to a self-hosted dashboard. Every
// failure here is a warning, never an error that reaches the user's exit
// code: a dashboard outage must not fail a CI build.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// timeout bounds the whole push. CI runs wait on this, so it is short.
const timeout = 15 * time.Second

type ToolStatus struct {
	Name    string `json:"name"`
	Skipped bool   `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

// envelope is the ingest wire format. Report is json.RawMessage so the CLI's
// report is embedded as an object exactly as rendered, never re-encoded.
type envelope struct {
	Branch string          `json:"branch"`
	Commit string          `json:"commit"`
	Tools  []ToolStatus    `json:"tools"`
	Report json.RawMessage `json:"report"`
}

// Send posts one run to the dashboard's ingest endpoint.
func Send(ctx context.Context, baseURL, token, branch, commit string, tools []ToolStatus, reportJSON []byte) error {
	if tools == nil {
		tools = []ToolStatus{}
	}
	body, err := json.Marshal(envelope{
		Branch: branch, Commit: commit, Tools: tools, Report: reportJSON,
	})
	if err != nil {
		return fmt.Errorf("encode push payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	url := strings.TrimSuffix(baseURL, "/") + "/api/runs"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("push to %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("dashboard returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// GitMeta reads the repo identity the dashboard keys runs on, straight out of
// the local checkout. All three commands are read-only.
func GitMeta(dir string) (remote, branch, commit string, err error) {
	if remote, err = git(dir, "config", "--get", "remote.origin.url"); err != nil {
		return "", "", "", fmt.Errorf("read git remote: %w", err)
	}
	if branch, err = git(dir, "rev-parse", "--abbrev-ref", "HEAD"); err != nil {
		return "", "", "", fmt.Errorf("read git branch: %w", err)
	}
	if commit, err = git(dir, "rev-parse", "HEAD"); err != nil {
		return "", "", "", fmt.Errorf("read git commit: %w", err)
	}
	return remote, branch, commit, nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", fmt.Errorf("git %s returned nothing", strings.Join(args, " "))
	}
	return value, nil
}
