// Package cache stores per-package analysis results between runs so an
// agent's fix-and-recheck loop only re-lints what actually changed.
//
// Invalidation is per package/crate, never per file: these tools reason at
// the package level, so a change in one file can alter a diagnostic reported
// against a different file in the same package. File-level invalidation
// would silently serve stale findings.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/finding"
)

// Fingerprint hashes the names, sizes and contents of the files in dir with
// one of the given extensions. It does not descend into subdirectories: each
// package/crate directory is fingerprinted on its own so a change in a child
// package invalidates only that child.
func Fingerprint(dir string, exts []string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	wanted := map[string]bool{}
	for _, e := range exts {
		wanted[e] = true
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !wanted[filepath.Ext(e.Name())] {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names) // ReadDir order is not guaranteed across platforms

	h := sha256.New()
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ToolStamp identifies the binary that will run, so replacing or upgrading a
// linter invalidates everything it cached. The binary's path, size and
// modification time stand in for a parsed --version string: same guarantee,
// no per-tool version-flag handling. A tool that cannot be resolved stamps
// as "missing", which never matches a stamp recorded from a real binary.
//
// Note that resolveCommand prefers PATH over the Go bin dir, so the same
// tool can resolve to a different path between runs and invalidate its own
// entries. That is deliberate: a spurious miss costs one re-lint, a spurious
// hit serves stale findings.
func ToolStamp(toolName string) string {
	path, ok := adapter.ResolveCommand(toolName)
	if !ok {
		return "missing"
	}
	info, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	return path + "|" + strconv.FormatInt(info.Size(), 10) + "|" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

type entry struct {
	Stamp       string            `json:"stamp"`
	Fingerprint string            `json:"fingerprint"`
	Findings    []finding.Finding `json:"findings"`
}

// Store is one repository's cache. Entries are held in memory during a run
// and flushed once by Save, so a run with many packages does not rewrite the
// file once per package.
type Store struct {
	file string

	mu      sync.Mutex
	entries map[string]entry // "tool\x00unit" -> entry
	dirty   bool
}

// Open loads (or starts) the cache for repoPath. A corrupt or unreadable
// cache file is treated as empty rather than fatal: a stale cache must never
// be able to stop an analysis from running.
func Open(repoPath string) (*Store, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(abs))
	dir, err := cacheDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	s := &Store{
		file:    filepath.Join(dir, hex.EncodeToString(sum[:16])+".json"),
		entries: map[string]entry{},
	}
	if b, err := os.ReadFile(s.file); err == nil {
		_ = json.Unmarshal(b, &s.entries)
	}
	return s, nil
}

// Root is where everything this tool caches lives: analysis results here,
// downloaded toolchains alongside. CODEBASE_ANALYSER_CACHE overrides it -
// os.UserCacheDir honours XDG_CACHE_HOME on Linux but nothing equivalent on
// macOS or Windows, so tests need a seam that works everywhere.
func Root() (string, error) {
	if override := os.Getenv("CODEBASE_ANALYSER_CACHE"); override != "" {
		return override, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "codebase-analyser"), nil
}

func cacheDir() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "analysis"), nil
}

func key(tool, unit string) string { return tool + "\x00" + unit }

// Get returns the cached findings for one tool against one package/crate,
// but only if both the tool binary and the package's contents are unchanged.
func (s *Store) Get(tool, stamp, unit, fingerprint string) ([]finding.Finding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key(tool, unit)]
	if !ok || e.Stamp != stamp || e.Fingerprint != fingerprint {
		return nil, false
	}
	return e.Findings, true
}

func (s *Store) Put(tool, stamp, unit, fingerprint string, fs []finding.Finding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key(tool, unit)] = entry{Stamp: stamp, Fingerprint: fingerprint, Findings: fs}
	s.dirty = true
	return nil
}

// Save flushes the cache. It writes to a temp file and renames, so an
// interrupted run leaves the previous cache intact rather than a half-written
// one.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	b, err := json.Marshal(s.entries)
	if err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.file); err != nil {
		return err
	}
	s.dirty = false
	return nil
}
