package adapter

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"codebase-analyser/internal/finding"
)

// DefaultTimeout is how long a single tool run may take before being killed.
const DefaultTimeout = 5 * time.Minute

// maxStderrInError caps how much stderr text gets embedded in a runCommand
// error, so one hung/verbose tool can't dump megabytes into a report.
const maxStderrInError = 500

// ToolAdapter wraps one external static-analysis tool.
type ToolAdapter interface {
	Name() string
	CheckInstalled() bool
	Install() error
	Run(path string) ([]finding.Finding, error)
}

// runCommand executes name with args in dir and returns stdout, respecting
// DefaultTimeout. Linters commonly exit non-zero when they find issues —
// that's not a run failure, so a non-zero exit is only treated as success
// when there's actual stdout to parse. A non-zero exit with no stdout is a
// real failure (bad flag, panic, missing config), so it's surfaced as an
// error carrying the exit code and captured stderr instead of being
// swallowed into a confusing downstream JSON-parse error.
func runCommand(dir, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if len(bytes.TrimSpace(out)) > 0 {
			return out, nil
		}
		stderr := bytes.TrimSpace(exitErr.Stderr)
		if len(stderr) > maxStderrInError {
			stderr = stderr[:maxStderrInError]
		}
		return out, fmt.Errorf("%s exited %d: %s", name, exitErr.ExitCode(), stderr)
	}
	return out, err
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
