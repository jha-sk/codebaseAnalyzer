package web

import (
	"io/fs"
	"strings"
	"testing"
)

// Guards the embed wiring: dist/ is a committed build artifact, so the way
// this breaks is someone changing the UI and forgetting to rebuild - which
// ships a binary serving a stale or missing page.
func TestBuiltAssetsAreEmbedded(t *testing.T) {
	index, err := fs.ReadFile(Assets, "index.html")
	if err != nil {
		t.Fatalf("dist/index.html is not embedded (run `npm run build` in ui/): %v", err)
	}
	if !strings.Contains(string(index), "<script") {
		t.Error("dist/index.html has no script tag; it looks like a placeholder, not a Vite build")
	}
	entries, err := fs.ReadDir(Assets, "assets")
	if err != nil || len(entries) == 0 {
		t.Errorf("dist/assets is empty or missing: %v", err)
	}
}
