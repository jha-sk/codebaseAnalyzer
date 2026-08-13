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

	projects, skipped, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want none", skipped)
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
	projects, _, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("got %d projects, want 0", len(projects))
	}
}

// TestDetectSkipsTestdataDir ensures the tool never analyses its own (or
// anyone else's) testdata/ tree, which by Go convention holds deliberately
// broken/fixture code that isn't a real project to scan.
func TestDetectSkipsTestdataDir(t *testing.T) {
	root := t.TempDir()
	fixtureDir := filepath.Join(root, "testdata", "fixtures", "go-repo")
	os.MkdirAll(fixtureDir, 0o755)
	os.WriteFile(filepath.Join(fixtureDir, "go.mod"), []byte("module fixture\n"), 0o644)

	projects, _, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("got %d projects under testdata/, want 0: %+v", len(projects), projects)
	}
}

// TestDetectUnreadableDirSkippedNotFatal is Important 1's regression test:
// a single permission-denied subdirectory must not abort the whole walk -
// the rest of the tree still gets scanned, and the unreadable path is
// reported back rather than silently dropped.
func TestDetectUnreadableDirSkippedNotFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits aren't enforced")
	}
	root := t.TempDir()

	goDir := filepath.Join(root, "svc")
	os.MkdirAll(goDir, 0o755)
	os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module svc\n"), 0o644)

	blocked := filepath.Join(root, "blocked")
	os.MkdirAll(blocked, 0o755)
	os.WriteFile(filepath.Join(blocked, "Cargo.toml"), []byte("[package]\n"), 0o644)
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0o755) })

	projects, skipped, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect returned an error, want the unreadable dir to be skipped, not fatal: %v", err)
	}
	if len(projects) != 1 || projects[0].Language != "go" {
		t.Fatalf("got %+v, want the one readable go project despite the unreadable sibling", projects)
	}
	if len(skipped) == 0 {
		t.Fatal("expected the unreadable directory to be reported in the skipped list")
	}
}
