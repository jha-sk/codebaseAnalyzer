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

var jsInstallOnce struct {
	sync.Once
	err error
}

// installJSTools npm-installs jsPinnedPackages into jsToolsDir and drops the
// baseline configs alongside them. All three JS adapters share the one
// install, so it runs at most once per process.
func installJSTools() error {
	jsInstallOnce.Do(func() { jsInstallOnce.err = doInstallJSTools() })
	return jsInstallOnce.err
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
