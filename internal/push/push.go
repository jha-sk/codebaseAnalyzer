// Package push sends a completed CLI run to a self-hosted dashboard. Every
// failure here is a warning, never an error that reaches the user's exit
// code: a dashboard outage must not fail a CI build.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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

// GitMeta reads the repo identity the dashboard keys runs on: branch and
// commit from the local checkout. It deliberately does not read the git
// remote - the dashboard identifies a repo by its ingest token, not by
// remote URL, so no caller ever used that value.
//
// Branch resolution is a fallback chain (see pickBranch) rather than a bare
// `git rev-parse --abbrev-ref HEAD`, because that command returns the
// literal string "HEAD" on a detached checkout - which is what
// actions/checkout, tag builds and most PR-build modes leave CI on. Taking
// that value at face value would collapse every branch's history into one
// meaningless "HEAD" row.
func GitMeta(dir string) (branch, commit string, err error) {
	revParseAbbrevRef, _ := git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	branchShowCurrent, _ := git(dir, "branch", "--show-current")
	branch, err = pickBranch(os.Getenv("GITHUB_REF_NAME"), os.Getenv("CI_COMMIT_REF_NAME"), revParseAbbrevRef, branchShowCurrent)
	if err != nil {
		return "", "", err
	}
	if commit, err = git(dir, "rev-parse", "HEAD"); err != nil {
		return "", "", fmt.Errorf("read git commit: %w", err)
	}
	return branch, commit, nil
}

// pickBranch is the pure fallback-chain logic behind GitMeta's branch
// resolution, factored out so it is testable without a real (or detached)
// git checkout. First usable candidate wins:
//  1. GITHUB_REF_NAME (GitHub Actions)
//  2. CI_COMMIT_REF_NAME (GitLab CI)
//  3. `git rev-parse --abbrev-ref HEAD`, rejected if it is literally "HEAD"
//     (the value a detached checkout returns)
//  4. `git branch --show-current`, rejected if empty
func pickBranch(githubRefName, gitlabRefName, revParseAbbrevRef, branchShowCurrent string) (string, error) {
	if githubRefName != "" {
		return githubRefName, nil
	}
	if gitlabRefName != "" {
		return gitlabRefName, nil
	}
	if revParseAbbrevRef != "" && revParseAbbrevRef != "HEAD" {
		return revParseAbbrevRef, nil
	}
	if branchShowCurrent != "" {
		return branchShowCurrent, nil
	}
	return "", errors.New("could not determine branch name: checkout is likely a detached HEAD (common in CI); set $GITHUB_REF_NAME or $CI_COMMIT_REF_NAME to override")
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
