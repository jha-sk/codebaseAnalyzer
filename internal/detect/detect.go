package detect

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type Project struct {
	Path     string
	Language string // "go", "rust", "js" or "ts"
}

// skipDirs are directory names the walk never descends into: VCS metadata,
// dependency trees, and build output. The JS/TS entries mirror
// adapter.jsExcludedDirs - the two lists are deliberately duplicated rather
// than shared, because detect importing adapter would invert the dependency
// direction the whole pipeline is built on (adapter knows nothing about
// detection, and cache already imports adapter).
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
}

// Detect walks root for go.mod / Cargo.toml / package.json, one Project per
// directory found. A repo may contain several languages, and one directory
// may be more than one project (a Go service with a JS build pipeline is
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
			if skipDirs[d.Name()] {
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
		}
		return nil
	})
	if err != nil {
		return nil, skipped, fmt.Errorf("detect: %w", err)
	}
	return projects, skipped, nil
}

// jsLanguage distinguishes a TypeScript package from a plain JavaScript one
// by the presence of a tsconfig.json beside the package.json. It is the only
// thing that decides whether the tsc adapter runs (spec: Detection).
func jsLanguage(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "tsconfig.json")); err == nil {
		return "ts"
	}
	return "js"
}
