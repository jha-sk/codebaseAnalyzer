package adapter

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// jsPinnedPackages is the exact tool set installed into the analyser's
// private cache. Pinned rather than floating (`npx eslint`) so repeat runs
// are reproducible instead of silently shifting when a tool publishes.
var jsPinnedPackages = []string{
	"eslint@9.39.0",
	"@eslint/js@9.39.0",
	"typescript@5.9.3",
	"typescript-eslint@8.46.0",
	"eslint-plugin-promise@7.2.1",
	"eslint-plugin-security@3.0.1",
	"globals@16.5.0",
}

// Baseline configs used only when a repo has no ESLint config of its own.
// Authored in both formats: the flat one for our pinned ESLint 9, the legacy
// one for when we fall back to a repo-local ESLint 8 that has no config.
//
//go:embed baseline/eslint.config.mjs
var baselineFlatConfig []byte

//go:embed baseline/eslintrc.json
var baselineLegacyConfig []byte

// jsToolsDir is where the pinned Node tooling lives: one shared install
// reused across runs, never the repo's own node_modules and never a global
// npm install.
// It mirrors cache.Root()'s rules, including the CODEBASE_ANALYSER_CACHE
// override, but cannot call it: internal/cache imports internal/adapter for
// ResolveCommand, so the dependency only runs one way.
func jsToolsDir() string {
	if override := os.Getenv("CODEBASE_ANALYSER_CACHE"); override != "" {
		return filepath.Join(override, "js-tools")
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "codebase-analyser", "js-tools")
}

// BaselineFlatConfigPath / BaselineLegacyConfigPath are where the embedded
// baselines get written at install time. They sit inside jsToolsDir so
// ESLint resolves the plugins they reference against the pinned install.
func baselineFlatConfigPath() string   { return filepath.Join(jsToolsDir(), "eslint.config.mjs") }
func baselineLegacyConfigPath() string { return filepath.Join(jsToolsDir(), ".eslintrc.json") }

var (
	jsInstallMu   sync.Mutex
	jsInstallDone bool
)

// jsInstallStep performs the actual install. It's a package-level function
// variable, not a direct call to doInstallJSTools, purely so tests can
// substitute a fake failing/succeeding step without touching the network -
// installJSTools itself always calls through it.
var jsInstallStep = doInstallJSTools

// installJSTools npm-installs jsPinnedPackages into jsToolsDir and drops the
// baseline configs alongside them. All three JS adapters share the one
// install.
//
// This used to run at most once per process via sync.Once, which is wrong
// for cmd/codebase-analyser-mcp: that's a long-running server, not a single
// CLI invocation, so one transient npm blip (network hiccup, registry
// timeout) on the very first `analyze` call would permanently disable
// ESLint and tsc for the rest of the process's life. A mutex plus a "done"
// flag instead: success is cached permanently (no repeat installs once one
// has succeeded), but a failure is retried by the next caller. The mutex
// also keeps the install serialised the same way sync.Once did - three
// adapters must not npm-install into the same jsToolsDir concurrently.
func installJSTools() error {
	jsInstallMu.Lock()
	defer jsInstallMu.Unlock()
	if jsInstallDone {
		return nil
	}
	if err := jsInstallStep(); err != nil {
		return err
	}
	jsInstallDone = true
	return nil
}

func doInstallJSTools() error {
	dir := jsToolsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("js tools cache: %w", err)
	}
	// npm refuses to treat a bare directory as an install root cleanly; a
	// minimal private manifest keeps it from walking up into the user's home.
	manifest := filepath.Join(dir, "package.json")
	if _, err := os.Stat(manifest); err != nil {
		if err := os.WriteFile(manifest, []byte(`{"name":"codebase-analyser-tools","private":true}`), 0o644); err != nil {
			return fmt.Errorf("js tools cache: %w", err)
		}
	}
	if err := os.WriteFile(baselineFlatConfigPath(), baselineFlatConfig, 0o644); err != nil {
		return fmt.Errorf("js baseline config: %w", err)
	}
	if err := os.WriteFile(baselineLegacyConfigPath(), baselineLegacyConfig, 0o644); err != nil {
		return fmt.Errorf("js baseline config: %w", err)
	}
	if !commandExists("npm") {
		return fmt.Errorf("npm not found on PATH (Node.js is required to analyse JS/TS projects)")
	}
	args := append([]string{"install", "--prefix", dir, "--no-audit", "--no-fund", "--loglevel=error"}, jsPinnedPackages...)
	cmd := exec.Command("npm", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("npm install: %w: %s", err, trimForError(out))
	}
	return nil
}

// pinnedJSBin is the path to a tool in the analyser's own install, whether
// or not it exists yet.
func pinnedJSBin(name string) string {
	return filepath.Join(jsToolsDir(), "node_modules", ".bin", name)
}

// jsBin resolves which copy of a tool to run for a project: the repo's own
// node_modules/.bin/<name> (searched from the project dir upwards, so a
// workspace member finds the hoisted root install) before the analyser's
// pinned copy. A repo that ships its own ESLint usually ships plugins and a
// config that only that copy can resolve. Returns "" if neither exists.
func jsBin(projectPath, name string) string {
	dir, err := filepath.Abs(projectPath)
	if err != nil {
		dir = projectPath
	}
	for {
		if candidate := filepath.Join(dir, "node_modules", ".bin", name); isExecutable(candidate) {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if p := pinnedJSBin(name); isExecutable(p) {
		return p
	}
	return ""
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// findUp returns the directory at or above start that contains name, or ""
// if none does. Used to locate a lockfile or workspace manifest that lives
// at the repo root rather than in the package being analysed.
func findUp(start, name string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		dir = start
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// boundedAncestorSearch walks dir and its ancestors, calling check at each
// level, stopping after the directory that contains .git (that directory
// itself is still checked first) or the filesystem root, whichever comes
// first. Used by lockfileExistsAbove (jsaudit.go), which needs to search
// upward without escaping the repo into the user's home directory or above -
// a stray lockfile there must not silently change behaviour for every repo
// on the machine. Mirrors the identical bounded walk repoHasESLintConfig
// (eslint.go) does for the same reason; that function is left as its own
// unshared loop rather than rewired onto this helper here.
func boundedAncestorSearch(dir string, check func(string) bool) bool {
	d, err := filepath.Abs(dir)
	if err != nil {
		d = dir
	}
	for {
		if check(d) {
			return true
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			// d is the repo root; stop here rather than walking past it.
			return false
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// jsExcludedDirs are the generated/vendored trees excluded from every JS/TS
// scan; left in, a typical repo's dependency tree dominates both scan time
// and noise. node_modules is ESLint's own default ignore, repeated here so
// the list is one thing rather than two.
var jsExcludedDirs = []string{"node_modules", "dist", "build", ".next", "out", "coverage"}

func trimForError(b []byte) string {
	if len(b) > maxStderrInError {
		b = b[:maxStderrInError]
	}
	return string(b)
}
