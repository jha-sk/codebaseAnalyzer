package cache

import (
	"io/fs"
	"os"
	"path/filepath"

	"codebase-analyser/internal/detect"
)

// Unit is one independently-cacheable piece of a project: a Go package
// directory, or a whole Rust crate. Dir is the absolute path fingerprinted;
// Target is what gets passed to the tool to restrict its run.
type Unit struct {
	Dir    string
	Target string
	Exts   []string
}

// goExts and rustExts are the file kinds whose contents can change a tool's
// diagnostics for a unit.
var (
	goExts   = []string{".go"}
	rustExts = []string{".rs", ".toml", ".lock"}
)

// Units enumerates a project's cacheable units. For Go that is every
// directory containing at least one .go file; for Rust it is the crate root,
// because clippy type-checks a crate as a whole and cannot report on less
// than one.
func Units(project detect.Project) ([]Unit, error) {
	switch project.Language {
	case "rust":
		return []Unit{{Dir: project.Path, Target: ".", Exts: rustExts}}, nil
	case "go":
		// falls through to the package walk below
	default:
		// JS/TS (and any future language) have no unit model here yet, and
		// none of their adapters implements Targeted, so the orchestrator
		// never asks. Returning nil here rather than falling into the Go
		// walk just stops that walk running (and finding nothing) against a
		// project it has no business inspecting.
		return nil, nil
	}

	var units []Unit
	err := filepath.WalkDir(project.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Mirror detect.Detect: an unreadable subtree is skipped, not fatal.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case ".git", "node_modules", "vendor", "target", "testdata":
			return filepath.SkipDir
		}
		if !hasExt(path, goExts) {
			return nil
		}
		rel, err := filepath.Rel(project.Path, path)
		if err != nil {
			return nil
		}
		target := "./" + filepath.ToSlash(rel)
		if rel == "." {
			target = "./"
		}
		units = append(units, Unit{Dir: path, Target: target, Exts: goExts})
		return nil
	})
	return units, err
}

func hasExt(dir string, exts []string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		for _, ext := range exts {
			if filepath.Ext(e.Name()) == ext {
				return true
			}
		}
	}
	return false
}
