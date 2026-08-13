package toolchain_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codebase-analyser/internal/toolchain"
)

// newMismatchServer serves a checksum that never matches the archive bytes
// it also serves, so any caller that verifies before extracting must fail.
func newMismatchServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000"))
			return
		}
		w.Write([]byte("not-a-real-archive"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEnsureGoReusesAnAlreadyExtractedToolchain(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("CODEBASE_ANALYSER_CACHE", cacheHome)

	// Pre-populate the cache the way a previous run would have.
	root := filepath.Join(cacheHome, "toolchains", "go", "1.26.5")
	bin := filepath.Join(root, "go", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bin, "go")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Point downloads at an address that cannot resolve: a cache hit must
	// never touch the network.
	t.Setenv("CODEBASE_ANALYSER_GO_DL", "http://127.0.0.1:1/")

	goroot, err := toolchain.EnsureGo("1.26.5")
	if err != nil {
		t.Fatalf("EnsureGo on a warm cache: %v", err)
	}
	if !strings.HasPrefix(goroot, root) {
		t.Errorf("goroot = %q, want it inside %q", goroot, root)
	}
}

func TestEnsureGoRejectsAChecksumMismatch(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	// A server that serves a body and a checksum that do not match.
	srv := newMismatchServer(t)
	t.Setenv("CODEBASE_ANALYSER_GO_DL", srv.URL+"/")

	if _, err := toolchain.EnsureGo("1.26.5"); err == nil {
		t.Fatal("err = nil, want a checksum-mismatch error")
	} else if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("err = %v, want it to name the checksum mismatch", err)
	}
}
