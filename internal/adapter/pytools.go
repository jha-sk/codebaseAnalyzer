package adapter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// pyPinnedPackages is the exact tool set installed into the analyser's
// private venv, pinned rather than floating - same reasoning as
// jsPinnedPackages (js.go): repeat runs stay reproducible instead of
// silently shifting when a tool publishes.
// ruff==0.16.3 is confirmed installed and current as of this plan (verified
// live via `ruff --version` in this environment); mypy==1.14.1 and
// pip-audit==2.9.0 are recent known-good releases from general knowledge,
// not verified live here (neither tool is installed in this sandbox) - if
// either version has since been yanked/superseded, doInstallPyTools's pip
// install will fail loudly and visibly rather than silently, so bump the
// pin here if that happens.
var pyPinnedPackages = []string{
	"ruff==0.16.3",
	"mypy==1.14.1",
	"pip-audit==2.9.0",
}

// pyToolsDir is where the pinned Python tooling venv lives: one shared
// install reused across runs, never the target repo's own environment and
// never a global pip install. Mirrors jsToolsDir()'s exact shape/reasoning
// (js.go) - it cannot call cache.Root() either, for the same import-cycle
// reason documented there.
func pyToolsDir() string {
	if override := os.Getenv("CODEBASE_ANALYSER_CACHE"); override != "" {
		return filepath.Join(override, "py-tools")
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "codebase-analyser", "py-tools")
}

// pinnedPyBin is the path to a tool in the analyser's own venv. Unlike
// jsBin, there is no repo-local equivalent to prefer first (no vendored
// ruff/mypy/pip-audit convention analogous to node_modules/.bin) - every
// Python adapter always runs the pinned copy, consistent with the "no
// dependency installation" spec constraint (a repo-local install would mean
// installing the repo's own dependencies first).
func pinnedPyBin(name string) string {
	return filepath.Join(pyToolsDir(), "bin", name)
}

var (
	pyInstallMu   sync.Mutex
	pyInstallDone bool
)

// pyInstallStep performs the actual install; a package-level func var
// (mirrors jsInstallStep in js.go) purely so tests can substitute a fake
// failing/succeeding step without touching the network or a real venv.
var pyInstallStep = doInstallPyTools

// installPyTools creates the venv and pip-installs pyPinnedPackages into
// it. All three Python adapters share the one install. Uses a mutex plus a
// "done" flag rather than sync.Once, same reasoning as installJSTools
// (js.go): success is cached permanently, but a failure is retried by the
// next caller rather than poisoning every future analyze call in a
// long-running MCP server process.
func installPyTools() error {
	pyInstallMu.Lock()
	defer pyInstallMu.Unlock()
	if pyInstallDone {
		return nil
	}
	if err := pyInstallStep(); err != nil {
		return err
	}
	pyInstallDone = true
	return nil
}

// pythonInterpreter picks whichever of python3/python is on PATH to build
// the venv with - read-only use, the system interpreter itself is never
// modified (only used once to create our own isolated venv).
func pythonInterpreter() (string, bool) {
	if commandExists("python3") {
		return "python3", true
	}
	if commandExists("python") {
		return "python", true
	}
	return "", false
}

func doInstallPyTools() error {
	dir := pyToolsDir()
	py, ok := pythonInterpreter()
	if !ok {
		return fmt.Errorf("python3 not found on PATH (Python is required to analyse Python projects)")
	}
	if err := exec.Command(py, "-m", "venv", dir).Run(); err != nil {
		return fmt.Errorf("py tools venv: %w", err)
	}
	pip := filepath.Join(dir, "bin", "pip")
	args := append([]string{"install", "--quiet"}, pyPinnedPackages...)
	cmd := exec.Command(pip, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pip install: %w: %s", err, trimForError(out))
	}
	return nil
}
