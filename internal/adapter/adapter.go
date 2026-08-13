package adapter

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codebase-analyser/internal/finding"
)

// DefaultTimeout is how long a single tool run may take before being killed.
const DefaultTimeout = 5 * time.Minute

// maxStderrInError caps how much stderr text gets embedded in a runCommand
// error, so one hung/verbose tool can't dump megabytes into a report.
const maxStderrInError = 500

// goBinDirOnce/goBinDirValue memoize the Go bin directory so every
// commandExists/runCommand call doesn't shell out to `go env` again.
var (
	goBinDirOnce  sync.Once
	goBinDirValue string
)

// computeGoBinDir resolves the Go bin directory by consulting the Go
// toolchain's own view via `go env GOBIN GOPATH`. This respects both
// process environment variables and persistent settings via `go env -w`.
// If `go` is not available, falls back to os.Getenv for process env vars.
// Returns "" if neither GOBIN nor GOPATH are set.
func computeGoBinDir() string {
	out, err := exec.Command("go", "env", "GOBIN", "GOPATH").Output()
	if err != nil {
		// `go` is not installed/reachable; fall back to process environment only.
		if gobin := os.Getenv("GOBIN"); gobin != "" {
			return gobin
		}
		if gopath := os.Getenv("GOPATH"); gopath != "" {
			return filepath.Join(gopath, "bin")
		}
		return ""
	}

	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) < 2 {
		return ""
	}

	// First line is GOBIN (may be empty if not explicitly set anywhere).
	if gobin := strings.TrimSpace(lines[0]); gobin != "" {
		return gobin
	}

	// Second line is GOPATH; use its bin subdir as fallback.
	if gopath := strings.TrimSpace(lines[1]); gopath != "" {
		return filepath.Join(gopath, "bin")
	}

	return ""
}

// goBinDir returns the directory `go install` places binaries in: $GOBIN if
// set, else $GOPATH/bin. `go install .../gosec@latest` (see gosec.go et al)
// puts gosec/golangci-lint/govulncheck there, but that directory is not on
// PATH by default - so a fresh auto-install "succeeds" (Install returns nil)
// and then Run fails with "executable file not found in $PATH". Returns ""
// if `go` isn't installed/reachable, which degrades to the pre-fix
// PATH-only behavior rather than panicking.
func goBinDir() string {
	goBinDirOnce.Do(func() {
		goBinDirValue = computeGoBinDir()
	})
	return goBinDirValue
}

// resolveCommand finds the executable to run for name: PATH first, falling
// back to goBinDir() for tools `go install` placed there but PATH doesn't
// see. commandExists must use the exact same resolution, or a tool can
// report itself "installed" via one path and then fail to exec via the
// other.
func resolveCommand(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	dir := goBinDir()
	if dir == "" {
		return "", false
	}
	candidate := filepath.Join(dir, name)
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return "", false
	}
	return candidate, true
}

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
	resolved, ok := resolveCommand(name)
	if !ok {
		// Keep the original name in the exec error rather than failing here:
		// this mirrors pre-fix behavior for a tool that's genuinely absent.
		resolved = name
	}
	cmd := exec.CommandContext(ctx, resolved, args...)
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
	_, ok := resolveCommand(name)
	return ok
}
