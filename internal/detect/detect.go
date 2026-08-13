package detect

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

type Project struct {
	Path     string
	Language string // "go" or "rust"
}

// Detect walks root for go.mod / Cargo.toml, one Project per directory found.
// A repo may contain both languages. The second return value lists any
// paths the walk couldn't read (e.g. permission-denied subdirectories) as
// "path: error" strings - the walk skips over them and keeps going rather
// than aborting the whole scan, but the caller can still surface that part
// of the tree wasn't examined instead of silently under-reporting.
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
			switch d.Name() {
			case ".git", "node_modules", "vendor", "target", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		switch d.Name() {
		case "go.mod":
			projects = append(projects, Project{Path: filepath.Dir(path), Language: "go"})
		case "Cargo.toml":
			projects = append(projects, Project{Path: filepath.Dir(path), Language: "rust"})
		}
		return nil
	})
	if err != nil {
		return nil, skipped, fmt.Errorf("detect: %w", err)
	}
	return projects, skipped, nil
}
