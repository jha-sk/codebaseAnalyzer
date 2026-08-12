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
// A repo may contain both languages.
func Detect(root string) ([]Project, error) {
	var projects []Project
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "target":
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
		return nil, fmt.Errorf("detect: %w", err)
	}
	return projects, nil
}
