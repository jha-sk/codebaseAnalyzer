package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetect(t *testing.T) {
	root := t.TempDir()
	goDir := filepath.Join(root, "svc")
	rustDir := filepath.Join(root, "sidecar")
	os.MkdirAll(goDir, 0o755)
	os.MkdirAll(rustDir, 0o755)
	os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module svc\n"), 0o644)
	os.WriteFile(filepath.Join(rustDir, "Cargo.toml"), []byte("[package]\n"), 0o644)

	projects, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2: %+v", len(projects), projects)
	}
	byLang := map[string]string{}
	for _, p := range projects {
		byLang[p.Language] = p.Path
	}
	if byLang["go"] != goDir {
		t.Errorf("go project path = %q, want %q", byLang["go"], goDir)
	}
	if byLang["rust"] != rustDir {
		t.Errorf("rust project path = %q, want %q", byLang["rust"], rustDir)
	}
}

func TestDetectNone(t *testing.T) {
	root := t.TempDir()
	projects, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("got %d projects, want 0", len(projects))
	}
}
