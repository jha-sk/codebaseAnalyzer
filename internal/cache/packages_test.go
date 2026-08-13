package cache_test

import (
	"os"
	"path/filepath"
	"testing"

	"codebase-analyser/internal/cache"
	"codebase-analyser/internal/detect"
)

func TestUnitsEnumeratesGoPackageDirectories(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module fixture\n\ngo 1.26\n")
	write(t, root, "main.go", "package main\n")
	sub := filepath.Join(root, "internal", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, sub, "p.go", "package pkg\n")
	// A directory with no Go files is not a package.
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	units, err := cache.Units(detect.Project{Path: root, Language: "go"})
	if err != nil {
		t.Fatal(err)
	}

	targets := map[string]bool{}
	for _, u := range units {
		targets[u.Target] = true
	}
	if !targets["./"] {
		t.Errorf("root package missing; got targets %v", targets)
	}
	if !targets["./internal/pkg"] {
		t.Errorf("nested package missing; got targets %v", targets)
	}
	if targets["./docs"] {
		t.Error("a directory with no Go files was treated as a package")
	}
}

func TestUnitsTreatsARustCrateAsOneUnit(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"fixture\"\nversion = \"0.1.0\"\n")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "src"), "main.rs", "fn main() {}\n")

	units, err := cache.Units(detect.Project{Path: root, Language: "rust"})
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 1 {
		t.Fatalf("len(units) = %d, want 1 (one crate, one unit)", len(units))
	}
}
