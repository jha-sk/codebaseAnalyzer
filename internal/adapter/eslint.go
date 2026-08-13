package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"codebase-analyser/internal/finding"
)

// ESLint has no package/crate-sized granularity the way Go packages or Rust
// crates do — a single `eslint .` invocation walks and lints the whole
// project tree in one process, so there is nothing smaller to restrict a run
// to that would line up with cache.Unit. RunTargets is therefore
// deliberately NOT implemented; ESLint always runs as a full-project Run.
type ESLint struct{}

func (ESLint) Name() string { return "eslint" }

func (ESLint) CheckInstalled() bool { return isExecutable(pinnedJSBin("eslint")) }

func (ESLint) Install() error { return installJSTools() }

type eslintMessage struct {
	RuleID   *string `json:"ruleId"`
	Severity int     `json:"severity"`
	Message  string  `json:"message"`
	Line     int     `json:"line"`
	Fatal    bool    `json:"fatal"`
}

type eslintFileResult struct {
	FilePath string          `json:"filePath"`
	Messages []eslintMessage `json:"messages"`
}

// eslintFindings parses ESLint's `-f json` formatter output. A null ruleId
// (ESLint's own shape for a fatal parse/config error) is reported under the
// rule id "fatal" rather than left blank, so it still shows up as a named
// row in a report instead of an empty cell. File is filePath verbatim — the
// orchestrator, not this function, normalises absolute paths to
// project-relative ones.
func eslintFindings(r io.Reader) ([]finding.Finding, error) {
	var results []eslintFileResult
	if err := json.NewDecoder(r).Decode(&results); err != nil {
		return nil, fmt.Errorf("eslint: parsing output: %w", err)
	}

	var findings []finding.Finding
	for _, res := range results {
		if len(res.Messages) == 0 {
			continue
		}
		for _, m := range res.Messages {
			classifyID, displayID := "", "fatal"
			if m.RuleID != nil && *m.RuleID != "" {
				classifyID, displayID = *m.RuleID, *m.RuleID
			}
			category, severity := classifyESLintRule(classifyID, m.Severity, m.Fatal)
			findings = append(findings, finding.Finding{
				File:     res.FilePath,
				Line:     m.Line,
				Tool:     "eslint",
				RuleID:   displayID,
				Category: category,
				Severity: severity,
				Message:  m.Message,
			})
		}
	}
	return findings, nil
}

// repoHasESLintConfig reports whether dir has its own ESLint configuration,
// in any of the forms ESLint recognises: flat config, legacy .eslintrc
// dotfile, or an "eslintConfig" key in package.json. If the repo has any of
// these the analyser must never override it with the baseline.
func repoHasESLintConfig(dir string) bool {
	names := []string{
		"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs",
		"eslint.config.ts", "eslint.config.mts", "eslint.config.cts",
		".eslintrc", ".eslintrc.js", ".eslintrc.cjs", ".eslintrc.yaml", ".eslintrc.yml", ".eslintrc.json",
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		ESLintConfig json.RawMessage `json:"eslintConfig"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	return len(pkg.ESLintConfig) > 0 && string(pkg.ESLintConfig) != "null"
}

// workspaceIgnoreGlobs returns the workspace member globs declared by dir's
// package.json ("workspaces", either the plain array form or the
// {"packages": [...]} object form npm also accepts) and/or its
// pnpm-workspace.yaml, or nil if dir declares no workspace. Each member
// package is detected as its own project and linted on its own, so linting
// it again from the root would report every finding twice under two
// different relative paths — hence these become --ignore-pattern globs.
func workspaceIgnoreGlobs(dir string) []string {
	var globs []string

	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if json.Unmarshal(data, &pkg) == nil && len(pkg.Workspaces) > 0 {
			var list []string
			if json.Unmarshal(pkg.Workspaces, &list) == nil {
				globs = append(globs, list...)
			} else {
				var obj struct {
					Packages []string `json:"packages"`
				}
				if json.Unmarshal(pkg.Workspaces, &obj) == nil {
					globs = append(globs, obj.Packages...)
				}
			}
		}
	}

	if data, err := os.ReadFile(filepath.Join(dir, "pnpm-workspace.yaml")); err == nil {
		globs = append(globs, pnpmWorkspaceGlobs(data)...)
	}

	if len(globs) == 0 {
		return nil
	}
	return globs
}

// pnpmWorkspaceGlobs reads the flat `packages:` list a pnpm-workspace.yaml
// declares, e.g.:
//
//	packages:
//	  - "apps/*"
//	  - "packages/*"
//
// A tiny line scanner rather than a YAML dependency: pnpm-workspace.yaml in
// practice is exactly this one flat list, nothing the analyser needs a full
// parser for.
func pnpmWorkspaceGlobs(data []byte) []string {
	var globs []string
	inPackages := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(stripYAMLComment(line))
		switch {
		case trimmed == "packages:":
			inPackages = true
		case inPackages && strings.HasPrefix(trimmed, "- "):
			glob := strings.Trim(strings.TrimPrefix(trimmed, "- "), `"'`)
			if glob != "" {
				globs = append(globs, glob)
			}
		case inPackages && trimmed != "" && !strings.HasPrefix(trimmed, "-"):
			inPackages = false
		}
	}
	return globs
}

// stripYAMLComment cuts a line off at the first '#' that isn't inside a
// quoted string, e.g. `packages: # comment` -> `packages: ` and
// `- "apps/*" # comment` -> `- "apps/*" `. A '#' inside a quoted glob (not a
// case pnpm-workspace.yaml actually needs, but cheap to get right) is left
// alone.
func stripYAMLComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return line[:i]
		}
	}
	return line
}

// eslintMajorVersion parses the major version out of `eslint --version`
// output (e.g. "v9.39.0"), or 0 if it can't be parsed.
func eslintMajorVersion(out string) int {
	s := strings.TrimPrefix(strings.TrimSpace(out), "v")
	parts := strings.SplitN(s, ".", 2)
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return n
}

func (ESLint) Run(path string) ([]finding.Finding, error) {
	bin := jsBin(path, "eslint")
	if bin == "" {
		return nil, fmt.Errorf("eslint not available")
	}

	// Routed through runCommand rather than exec.Command directly, like the
	// real lint invocation below: this is the same hook toolchain uses to
	// pin a project's Node version (EnvForPath) and the same DefaultTimeout,
	// not a separate unguarded subprocess.
	verOut, _ := runCommand(path, bin, "--version")
	major := eslintMajorVersion(string(verOut))

	args := []string{"-f", "json"}

	if !repoHasESLintConfig(path) {
		// No config of the repo's own: apply the analyser's baseline.
		// Format depends on the resolved binary's major version — v9
		// dropped .eslintrc support entirely, v8 doesn't understand flat
		// config. If the version can't be determined, assume flat: our
		// pinned copy of ESLint is 9.x.
		if major != 0 && major <= 8 {
			args = append(args, "--no-eslintrc", "--config", baselineLegacyConfigPath(), "--resolve-plugins-relative-to", jsToolsDir())
		} else {
			args = append(args, "--no-config-lookup", "--config", baselineFlatConfigPath())
		}
	}
	// v9 takes its file globs from the config itself and rejects --ext
	// outright; v8 has no other way to know which extensions to lint.
	if major != 0 && major <= 8 {
		args = append(args, "--ext", ".js,.jsx,.mjs,.cjs,.ts,.tsx,.mts,.cts")
	}

	for _, dir := range jsExcludedDirs {
		args = append(args, "--ignore-pattern", dir+"/**")
	}
	// Workspace member packages are detected as their own projects and
	// linted on their own by the orchestrator, so linting them again from
	// the root would report every finding twice under two different
	// relative paths.
	for _, glob := range workspaceIgnoreGlobs(path) {
		args = append(args, "--ignore-pattern", glob+"/**")
	}

	args = append(args, ".")

	// ESLint exits 1 when it reports problems; runCommand already treats a
	// non-zero exit with stdout present as success, not a run failure.
	out, err := runCommand(path, bin, args...)
	if err != nil {
		return nil, fmt.Errorf("eslint: %w", err)
	}
	return eslintFindings(bytes.NewReader(out))
}
