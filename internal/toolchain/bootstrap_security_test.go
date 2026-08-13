package toolchain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests cover the two extraction defences that the behaviour-level
// tests cannot reach: an archive is untrusted input, and both a crafted entry
// name and a symlink entry are ways to write outside the destination. Neither
// is exercised by EnsureGo's tests, which never get as far as a real archive.

func TestSafeJoinRejectsEscapingEntries(t *testing.T) {
	dest := t.TempDir()

	for _, name := range []string{
		"../escaped.txt",
		"../../escaped.txt",
		"go/../../escaped.txt",
		"go/./../../escaped.txt",
	} {
		if got, err := safeJoin(dest, name); err == nil {
			t.Errorf("safeJoin(%q) = %q, nil; want an error - the entry escapes the destination", name, got)
		}
	}

	// Absolute entry names are neutralised rather than rejected: filepath.Join
	// strips the leading separator, so "/etc/passwd" lands at dest/etc/passwd.
	// That is contained, which is what matters - the guard exists to keep
	// writes inside dest, not to police entry names for tidiness.
	//
	// Legitimate entries must still resolve too, or the guard is useless.
	for _, name := range []string{"go/bin/go", "go/./bin/go", "README", "/etc/passwd"} {
		got, err := safeJoin(dest, name)
		if err != nil {
			t.Errorf("safeJoin(%q) rejected an entry that stays inside dest: %v", name, err)
			continue
		}
		if !strings.HasPrefix(got, filepath.Clean(dest)+string(os.PathSeparator)) {
			t.Errorf("safeJoin(%q) = %q, which is outside %q", name, got, dest)
		}
	}
}

// tarGz builds a gzipped tar in memory from the given entries.
func tarGz(t *testing.T, entries []tar.Header, bodies []string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i, hdr := range entries {
		h := hdr
		h.Size = int64(len(bodies[i]))
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(bodies[i])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractTarGzRejectsTraversalEntry(t *testing.T) {
	archive := tarGz(t,
		[]tar.Header{{Name: "../escaped.txt", Mode: 0o644, Typeflag: tar.TypeReg}},
		[]string{"pwned"},
	)
	dest := filepath.Join(t.TempDir(), "extract")

	err := extractTarGz(archive, dest)
	if err == nil {
		t.Fatal("extractTarGz accepted an entry escaping the destination")
	}
	if !strings.Contains(err.Error(), "escapes destination") {
		t.Errorf("err = %v, want it to name the escaping entry", err)
	}

	// The sibling of dest is where "../escaped.txt" would have landed.
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "escaped.txt")); statErr == nil {
		t.Error("the escaping entry was written to disk outside the destination")
	}
}

func TestExtractTarGzSkipsSymlinksButKeepsRegularFiles(t *testing.T) {
	archive := tarGz(t,
		[]tar.Header{
			{Name: "go/bin/go", Mode: 0o755, Typeflag: tar.TypeReg},
			{Name: "go/evil", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		},
		[]string{"#!/bin/sh\n", ""},
	)
	dest := filepath.Join(t.TempDir(), "extract")

	if err := extractTarGz(archive, dest); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "go", "bin", "go")); err != nil {
		t.Errorf("the regular file was not extracted: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "go", "evil")); err == nil {
		t.Error("a symlink entry was created; symlinks must be skipped outright")
	}
}
