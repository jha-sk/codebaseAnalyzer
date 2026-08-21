package detect

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Project struct {
	Path     string
	Language string // "go", "rust", "js", "ts" or "python"
}

// skipDirs are directory names the walk never descends into: VCS metadata,
// dependency trees, and build output. The JS/TS entries mirror
// adapter.jsExcludedDirs - the two lists are deliberately duplicated rather
// than shared, because detect importing adapter would invert the dependency
// direction the whole pipeline is built on (adapter knows nothing about
// detection, and cache already imports adapter).
//
// The Python entries (.venv, venv, __pycache__, .mypy_cache, .ruff_cache)
// have no adapter-side equivalent to mirror since there's no per-project
// Python venv this analyser ever creates itself - these are just the
// conventional dirs a Python project's OWN tooling creates. *.egg-info is a
// variable-prefix pattern (package-name-derived) so it's handled separately
// below via strings.HasSuffix, not as a map entry - skipDirs only does
// exact-name matching.
//
// Note this widens the skip list for Go and Rust too: a go.mod under
// dist/ or build/ stops being detected. That is the intended reading of
// "generated output is not a project" and matches how testdata/ and
// vendor/ are already treated.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"target":       true,
	"testdata":     true,
	"dist":         true,
	"build":        true,
	".next":        true,
	"out":          true,
	"coverage":     true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	".mypy_cache":  true,
	".ruff_cache":  true,
}

// Detect walks root for go.mod / Cargo.toml / package.json / Python manifests,
// one Project per directory found. A repo may contain several languages, and one
// directory may be more than one project (a Go service with a JS build pipeline is
// both). The second return value lists any paths the walk couldn't read
// (e.g. permission-denied subdirectories) as "path: error" strings - the
// walk skips over them and keeps going rather than aborting the whole scan,
// but the caller can still surface that part of the tree wasn't examined
// instead of silently under-reporting.
func Detect(root string) ([]Project, []string, error) {
	var projects []Project
	var skipped []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", path, err))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || strings.HasSuffix(d.Name(), ".egg-info") {
				return filepath.SkipDir
			}
			return nil
		}
		dir := filepath.Dir(path)
		switch d.Name() {
		case "go.mod":
			projects = append(projects, Project{Path: dir, Language: "go"})
		case "Cargo.toml":
			projects = append(projects, Project{Path: dir, Language: "rust"})
		case "package.json":
			projects = append(projects, Project{Path: dir, Language: jsLanguage(dir)})
		case "pyproject.toml":
			projects = append(projects, Project{Path: dir, Language: "python"})
		case "requirements.txt":
			// Only add if pyproject.toml doesn't already claim this
			// directory - a Poetry/PEP621 project commonly also ships a
			// frozen requirements.txt for deployment, and WalkDir visits a
			// directory's files in lexical order (pyproject.toml <
			// requirements.txt alphabetically), so pyproject.toml is
			// always seen first when both exist.
			if !hasFile(dir, "pyproject.toml") {
				projects = append(projects, Project{Path: dir, Language: "python"})
			}
		case "setup.py":
			// Same dedup, one priority level lower: only add if NEITHER
			// pyproject.toml NOR requirements.txt already claimed this
			// directory. Lexical order (pyproject.toml < requirements.txt
			// < setup.py) guarantees both higher-priority markers, if
			// present, were already visited and are on disk to check for.
			if !hasFile(dir, "pyproject.toml") && !hasFile(dir, "requirements.txt") {
				projects = append(projects, Project{Path: dir, Language: "python"})
			}
		}
		return nil
	})
	if err != nil {
		return nil, skipped, fmt.Errorf("detect: %w", err)
	}
	return projects, skipped, nil
}

// hasFile reports whether dir directly contains a file named name.
func hasFile(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// jsLanguage distinguishes a TypeScript package from a plain JavaScript one
// by the presence of a tsconfig.json beside the package.json. It is the only
// thing that decides whether the tsc adapter runs (spec: Detection).
//
// Only a confirmed absence (os.IsNotExist) counts as "no tsconfig.json".
// Any other stat error - permission denied being the practical case - must
// not silently downgrade a real TypeScript project to plain JS just because
// its tsconfig.json couldn't be statted; that would quietly skip the
// project's type-check.
func jsLanguage(dir string) string {
	_, err := os.Stat(filepath.Join(dir, "tsconfig.json"))
	if err == nil || !os.IsNotExist(err) {
		return "ts"
	}
	return "js"
}
