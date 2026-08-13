package cache_test

import (
	"os"
	"path/filepath"
	"testing"

	"codebase-analyser/internal/cache"
	"codebase-analyser/internal/finding"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFingerprintChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.go", "package a\n")

	first, err := cache.Fingerprint(dir, []string{".go"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := cache.Fingerprint(dir, []string{".go"})
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Error("fingerprint is not stable across calls on unchanged files")
	}

	write(t, dir, "a.go", "package a\n\nvar x = 1\n")
	changed, err := cache.Fingerprint(dir, []string{".go"})
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Error("fingerprint did not change after editing a file")
	}
}

func TestFingerprintIgnoresIrrelevantExtensions(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.go", "package a\n")
	before, _ := cache.Fingerprint(dir, []string{".go"})

	write(t, dir, "README.md", "docs")
	after, _ := cache.Fingerprint(dir, []string{".go"})

	if before != after {
		t.Error("a non-.go file changed the .go fingerprint")
	}
}

func TestFingerprintDoesNotDescendIntoSubdirectories(t *testing.T) {
	// Invalidation is per package/crate: a change in a child package must
	// not invalidate its parent's entry, or the cache degrades to all-or-nothing.
	dir := t.TempDir()
	write(t, dir, "a.go", "package a\n")
	sub := filepath.Join(dir, "child")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	before, _ := cache.Fingerprint(dir, []string{".go"})

	write(t, sub, "b.go", "package child\n")
	after, _ := cache.Fingerprint(dir, []string{".go"})

	if before != after {
		t.Error("a file in a child directory changed the parent's fingerprint")
	}
}

func TestStoreRoundTripsAcrossReopen(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	repo := t.TempDir()
	want := []finding.Finding{{File: "a.go", Line: 2, Tool: "fake", RuleID: "R1",
		Category: finding.CategorySecurity, Severity: finding.SeverityHigh, Message: "m"}}

	s, err := cache.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put("fake", "stamp-1", "./pkg", "fp-1", want); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	reopened, err := cache.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Get("fake", "stamp-1", "./pkg", "fp-1")
	if !ok {
		t.Fatal("entry not found after reopening the store")
	}
	if len(got) != 1 || got[0].RuleID != "R1" || got[0].Severity != finding.SeverityHigh {
		t.Errorf("round-tripped findings = %+v, want %+v", got, want)
	}
}

func TestStoreMissesOnChangedFingerprintOrStamp(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	s, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Put("fake", "stamp-1", "./pkg", "fp-1", []finding.Finding{{File: "a.go"}})

	if _, ok := s.Get("fake", "stamp-1", "./pkg", "fp-2"); ok {
		t.Error("hit on a changed fingerprint; the source changed and the entry is stale")
	}
	if _, ok := s.Get("fake", "stamp-2", "./pkg", "fp-1"); ok {
		t.Error("hit on a changed tool stamp; the linter was replaced and the entry is stale")
	}
}

func TestSeparateReposDoNotShareEntries(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	a, _ := cache.Open(t.TempDir())
	b, _ := cache.Open(t.TempDir())
	a.Put("fake", "s", "./pkg", "fp", []finding.Finding{{File: "a.go"}})
	a.Save()

	if _, ok := b.Get("fake", "s", "./pkg", "fp"); ok {
		t.Error("a second repo saw the first repo's cached findings")
	}
}
