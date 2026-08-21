// internal/adapter/javatools_test.go
package adapter

import (
	"archive/zip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestJavaToolsDir_honorsCacheOverride(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", "/tmp/fake-cache-root")
	want := filepath.Join("/tmp/fake-cache-root", "java-tools")
	if got := javaToolsDir(); got != want {
		t.Errorf("javaToolsDir() = %q, want %q", got, want)
	}
}

func TestPinnedJavaBin_pathShape(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", "/tmp/fake-cache-root")
	want := filepath.Join("/tmp/fake-cache-root", "java-tools", "pmd", "bin", "pmd")
	if got := pinnedJavaBin("pmd", "bin/pmd"); got != want {
		t.Errorf("pinnedJavaBin(\"pmd\", \"bin/pmd\") = %q, want %q", got, want)
	}
}

// TestInstallJavaTool_retriesAfterFailure mirrors installPyTools's own
// retry test: a transient failure must not permanently poison installs for
// the rest of a long-running process.
func TestInstallJavaTool_retriesAfterFailure(t *testing.T) {
	original := javaInstallStep
	t.Cleanup(func() { javaInstallStep = original; javaInstallDone = map[string]bool{} })

	calls := 0
	javaInstallStep = func(tool string) error {
		calls++
		if calls == 1 {
			return fmt.Errorf("simulated failure")
		}
		return nil
	}
	javaInstallDone = map[string]bool{}

	if err := installJavaTool("pmd"); err == nil {
		t.Fatal("first installJavaTool(\"pmd\") = nil error, want the simulated failure")
	}
	if err := installJavaTool("pmd"); err != nil {
		t.Fatalf("second installJavaTool(\"pmd\") = %v, want success (retry after failure)", err)
	}
	if calls != 2 {
		t.Errorf("javaInstallStep called %d times, want 2", calls)
	}
}

// TestInstallJavaTool_perToolIndependence: installing "pmd" must not mark
// "checkstyle" as done too — each of the four tools installs independently
// (spec: a compile failure only skips SpotBugs, so PMD/Checkstyle/
// Dependency-Check's own install outcomes must never be entangled with
// SpotBugs's).
func TestInstallJavaTool_perToolIndependence(t *testing.T) {
	original := javaInstallStep
	t.Cleanup(func() { javaInstallStep = original; javaInstallDone = map[string]bool{} })

	calledFor := map[string]int{}
	javaInstallStep = func(tool string) error { calledFor[tool]++; return nil }
	javaInstallDone = map[string]bool{}

	if err := installJavaTool("pmd"); err != nil {
		t.Fatal(err)
	}
	if err := installJavaTool("checkstyle"); err != nil {
		t.Fatal(err)
	}
	if calledFor["pmd"] != 1 || calledFor["checkstyle"] != 1 {
		t.Errorf("calledFor = %+v, want each tool installed exactly once independently", calledFor)
	}
}

func TestDownloadVerified_rejectsChecksumMismatch(t *testing.T) {
	// A body whose real sha256 will never equal this literal.
	body := []byte("not-a-real-archive")
	srv := newFakeFileServer(t, body)
	defer srv.Close()

	_, err := downloadVerified(srv.URL, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("downloadVerified with a wrong checksum = nil error, want a checksum-mismatch error")
	}
}

func TestExtractZip_andFlattenSingleSubdir(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "archive.zip")
	writeTestZip(t, zipPath, map[string]string{
		"pmd-bin-7.8.0/bin/pmd":        "#!/bin/sh\necho pmd\n",
		"pmd-bin-7.8.0/lib/pmd-7.8.0.jar": "not a real jar",
	})

	dest := filepath.Join(dir, "installed")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(zipPath, dest); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	if err := flattenSingleSubdir(dest); err != nil {
		t.Fatalf("flattenSingleSubdir: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "bin", "pmd")); err != nil {
		t.Errorf("after flatten, bin/pmd not found directly under dest: %v", err)
	}
}

func TestExtractZip_rejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "evil.zip")
	writeTestZip(t, zipPath, map[string]string{
		"../../etc/passwd": "pwned",
	})

	dest := filepath.Join(dir, "installed")
	os.MkdirAll(dest, 0o755)
	if err := extractZip(zipPath, dest); err == nil {
		t.Fatal("extractZip with a path-traversal entry = nil error, want it rejected")
	}
}

// writeTestZip writes a zip archive at path containing entries (name ->
// content), used only to exercise extractZip without a real network fetch.
func writeTestZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func newFakeFileServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}
