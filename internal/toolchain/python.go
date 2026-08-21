package toolchain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// pythonLatestStable is used when a Python project declares no version of
// its own (neither .python-version nor pyproject.toml's requires-python).
// ponytail: hand-maintained constant; bump it as new CPython versions ship,
// or replace with a live lookup if that becomes tedious.
const pythonLatestStable = "3.12.7"

// requiresPythonKey matches pyproject.toml's `requires-python = "..."`
// entry. A three-line regex beats a TOML dependency for one key in one file
// - same tradeoff channelKey (rust.go) makes for rust-toolchain.toml.
var requiresPythonKey = regexp.MustCompile(`(?m)^[ \t]*requires-python[ \t]*=[ \t]*["']([^"']+)`)

// firstVersionNumber pulls the first concrete version out of a PEP 440
// constraint string like ">=3.10,<4" -> "3.10" - the constraint's lower
// bound, i.e. the oldest syntax/stdlib the project claims to support.
var firstVersionNumber = regexp.MustCompile(`[0-9]+(?:\.[0-9]+){1,2}`)

// pythonMarkers are the files that make a directory a Python project at
// all - mirrors detect.Detect's Python detection (internal/detect cannot be
// imported here: internal/detect has no dependency on internal/toolchain to
// begin with, but duplicating this ~5-line check is simpler and more
// consistent with this codebase's existing style than introducing a shared
// micro-package for it, the same call skipDirs/jsExcludedDirs already make).
var pythonMarkers = []string{"pyproject.toml", "requirements.txt", "setup.py"}

// Python resolves the interpreter version a repository declares.
type Python struct{}

// Detect deliberately differs from Go/Rust's "ok=false means run with
// whatever's on PATH" contract: once a directory is confirmed to be a
// Python project at all, Detect always returns ok=true, falling back to
// pythonLatestStable rather than leaving the version unpinned - per spec,
// "falls back to latest stable if neither is present". ok=false is reserved
// for "this isn't a Python project", so Env() (which runs every resolver
// against every repo path unconditionally) stays silent for a Go/Rust/JS
// repo it's also asked about.
func (Python) Detect(repoPath string) (string, bool) {
	if !isPythonProject(repoPath) {
		return "", false
	}
	if b, err := os.ReadFile(filepath.Join(repoPath, ".python-version")); err == nil {
		if v := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0]); v != "" {
			return v, true
		}
	}
	if b, err := os.ReadFile(filepath.Join(repoPath, "pyproject.toml")); err == nil {
		if m := requiresPythonKey.FindSubmatch(b); m != nil {
			if v := firstVersionNumber.Find(m[1]); v != nil {
				return string(v), true
			}
		}
	}
	return pythonLatestStable, true
}

func isPythonProject(repoPath string) bool {
	for _, name := range pythonMarkers {
		if _, err := os.Stat(filepath.Join(repoPath, name)); err == nil {
			return true
		}
	}
	return false
}

// Ensure prefers pyenv, the reference implementation of the
// .python-version convention Detect reads: `pyenv install --skip-existing`
// fetches a missing version through pyenv's own build process - same
// reasoning as Go's GOTOOLCHAIN and Rust's rustup, the ecosystem already
// ships this machinery.
//
// With no pyenv on PATH, it falls back to EnsurePython, a Python we
// download and manage ourselves. That fallback only knows a small, pinned
// set of versions (pythonBuildAssets in bootstrap.go) - an unlisted version
// fails clearly rather than guessing at a download URL.
func (Python) Ensure(version string) ([]string, error) {
	if _, err := exec.LookPath("pyenv"); err == nil {
		return ensureViaPyenv(version)
	}
	root, err := EnsurePython(version)
	if err != nil {
		return nil, err
	}
	return []string{
		"PATH=" + filepath.Join(root, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, nil
}

func ensureViaPyenv(version string) ([]string, error) {
	if err := exec.Command("pyenv", "install", "--skip-existing", version).Run(); err != nil {
		return nil, fmt.Errorf("pyenv install %s: %w", version, err)
	}
	out, err := exec.Command("pyenv", "prefix", version).Output()
	if err != nil {
		return nil, fmt.Errorf("pyenv prefix %s: %w", version, err)
	}
	prefix := strings.TrimSpace(string(out))
	return []string{
		"PATH=" + filepath.Join(prefix, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, nil
}
