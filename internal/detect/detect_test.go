package detect

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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

func TestDetectJSAndTS(t *testing.T) {
	root := t.TempDir()
	jsDir := filepath.Join(root, "web")
	tsDir := filepath.Join(root, "api")
	os.MkdirAll(jsDir, 0o755)
	os.MkdirAll(tsDir, 0o755)
	os.WriteFile(filepath.Join(jsDir, "package.json"), []byte(`{"name":"web"}`), 0o644)
	os.WriteFile(filepath.Join(tsDir, "package.json"), []byte(`{"name":"api"}`), 0o644)
	os.WriteFile(filepath.Join(tsDir, "tsconfig.json"), []byte(`{}`), 0o644)

	projects, _, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	byLang := map[string]string{}
	for _, p := range projects {
		byLang[p.Language] = p.Path
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2: %+v", len(projects), projects)
	}
	if byLang["js"] != jsDir {
		t.Errorf("js project path = %q, want %q", byLang["js"], jsDir)
	}
	// A package.json with a tsconfig.json beside it is a TypeScript project:
	// that is what enables the tsc step, and it must not also be reported
	// as a plain "js" project or ESLint would run against it twice.
	if byLang["ts"] != tsDir {
		t.Errorf("ts project path = %q, want %q", byLang["ts"], tsDir)
	}
}

// TestDetectSkipsGeneratedDirs covers the spec's excluded paths: a
// package.json inside a dependency tree or a build output directory is not a
// project anyone asked to analyse.
func TestDetectSkipsGeneratedDirs(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"app"}`), 0o644)
	for _, dir := range []string{"node_modules/left-pad", "dist", "build", ".next", "out", "coverage"} {
		full := filepath.Join(root, filepath.FromSlash(dir))
		os.MkdirAll(full, 0o755)
		os.WriteFile(filepath.Join(full, "package.json"), []byte(`{"name":"junk"}`), 0o644)
	}

	projects, _, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Path != root {
		t.Fatalf("got %+v, want only the root package", projects)
	}
}

// TestDetectMonorepoMembers is the workspace case from the spec: every
// member package is its own analyzable unit, found by the same recursive
// walk rather than by parsing the workspaces globs.
func TestDetectMonorepoMembers(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"mono","workspaces":["packages/*"]}`), 0o644)
	for _, name := range []string{"a", "b"} {
		dir := filepath.Join(root, "packages", name)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"`+name+`"}`), 0o644)
	}

	projects, _, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 3 {
		t.Fatalf("got %d projects, want 3 (root + 2 members): %+v", len(projects), projects)
	}
	var paths []string
	for _, p := range projects {
		if p.Language != "js" {
			t.Errorf("project %q language = %q, want js", p.Path, p.Language)
		}
		paths = append(paths, p.Path)
	}
	sort.Strings(paths)
	want := []string{root, filepath.Join(root, "packages", "a"), filepath.Join(root, "packages", "b")}
	sort.Strings(want)
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

// TestDetectGoAndJSInSameDir: a repo whose root holds both a Go module and a
// package.json (a Go service with a JS build pipeline) is both projects, so
// both toolchains run against it.
func TestDetectGoAndJSInSameDir(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module svc\n"), 0o644)
	os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"svc-ui"}`), 0o644)

	projects, _, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2: %+v", len(projects), projects)
	}
}

// TestDetectJSFixture covers Task 1's plain-JS case: a package.json with no
// tsconfig.json beside it is Language "js".
func TestDetectJSFixture(t *testing.T) {
	projects, skipped, err := Detect("../../testdata/fixtures/js-repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want none", skipped)
	}
	if len(projects) != 1 || projects[0].Language != "js" {
		t.Fatalf("got %+v, want exactly one js project", projects)
	}
}

// TestDetectTSFixture covers Task 1's TS case: a tsconfig.json beside
// package.json makes it Language "ts", not "js".
func TestDetectTSFixture(t *testing.T) {
	projects, _, err := Detect("../../testdata/fixtures/ts-repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Language != "ts" {
		t.Fatalf("got %+v, want exactly one ts project", projects)
	}
}

// TestDetectJSMonorepoFixture covers the workspace case: Detect has no
// special workspace-awareness, so a root package.json plus two member
// package.json files under packages/ must surface as three separate
// projects. Asserted by sorted path, since WalkDir order (and therefore
// projects order) is not part of Detect's contract.
func TestDetectJSMonorepoFixture(t *testing.T) {
	projects, _, err := Detect("../../testdata/fixtures/js-monorepo")
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 3 {
		t.Fatalf("got %d projects, want 3 (root + packages/a + packages/b): %+v", len(projects), projects)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Path < projects[j].Path })

	wantSuffixes := []string{
		"js-monorepo",
		filepath.Join("js-monorepo", "packages", "a"),
		filepath.Join("js-monorepo", "packages", "b"),
	}
	for i, want := range wantSuffixes {
		if !strings.HasSuffix(projects[i].Path, want) {
			t.Errorf("projects[%d].Path = %q, want suffix %q", i, projects[i].Path, want)
		}
		if projects[i].Language != "js" {
			t.Errorf("projects[%d].Language = %q, want %q", i, projects[i].Language, "js")
		}
	}
}

// TestJSLanguage_PermissionDeniedTsconfigNotTreatedAsAbsent is Minor 5's
// regression test: jsLanguage used to treat ANY os.Stat error on
// tsconfig.json - including permission denied - as "no tsconfig.json here",
// silently downgrading a real TypeScript project to plain JS (and so
// skipping its tsc type-check) whenever the file merely couldn't be
// statted. Only a confirmed absence (os.IsNotExist) may report "js"; any
// other stat error must still report "ts" rather than guess absence.
func TestJSLanguage_PermissionDeniedTsconfigNotTreatedAsAbsent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits aren't enforced")
	}
	root := t.TempDir()
	pkg := filepath.Join(root, "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Strip traversal permission on pkg itself so os.Stat(pkg/tsconfig.json)
	// fails with permission denied rather than not-exist.
	if err := os.Chmod(pkg, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(pkg, 0o755) })

	if got := jsLanguage(pkg); got != "ts" {
		t.Errorf("jsLanguage(permission-denied dir) = %q, want %q (must not silently downgrade to js)", got, "ts")
	}
}
