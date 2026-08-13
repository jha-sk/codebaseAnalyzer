package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/detect"
	"codebase-analyser/internal/finding"
)

type ToolResult struct {
	Tool     string
	Path     string // the project path this tool ran against
	Findings []finding.Finding
	Skipped  bool
	Error    error
}

var DefaultAdapters = map[string][]adapter.ToolAdapter{
	"go":   {adapter.GolangciLint{}, adapter.Gosec{}, adapter.Govulncheck{}},
	"rust": {adapter.Clippy{}, adapter.CargoAudit{}},
}

// Cache lets Run serve part of a tool's work from a previous run. It is an
// interface declared here, rather than an import of internal/cache, so the
// orchestrator stays independent of where results are stored.
type Cache interface {
	// Lookup splits one (tool, project) pair into what is already known and
	// what still has to run. ok=false means "do not cache this pair" and the
	// tool runs in full.
	Lookup(tool string, p detect.Project) (stale []string, cached []finding.Finding, ok bool)

	// Store records what a real run produced. ran is the target list that was
	// actually run, so units that produced no findings are still recorded as
	// clean rather than being re-run forever.
	Store(tool string, p detect.Project, ran []string, produced []finding.Finding)
}

// Run executes every adapter for every detected project concurrently, with no
// caching. See RunWithCache.
func Run(projects []detect.Project, adaptersByLang map[string][]adapter.ToolAdapter) []ToolResult {
	return RunWithCache(projects, adaptersByLang, nil)
}

// RunWithCache is Run with an optional cache. An adapter that does not
// implement adapter.Targeted cannot be restricted to a subset of a project,
// so it always runs in full and never consults c.
func RunWithCache(projects []detect.Project, adaptersByLang map[string][]adapter.ToolAdapter, c Cache) []ToolResult {
	var wg sync.WaitGroup
	resultsCh := make(chan ToolResult)
	inst := newInstaller()

	for _, p := range projects {
		for _, a := range adaptersByLang[p.Language] {
			wg.Add(1)
			go func(a adapter.ToolAdapter, p detect.Project) {
				defer wg.Done()
				resultsCh <- runOne(a, p, inst, c)
			}(a, p)
		}
	}

	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	var results []ToolResult
	for r := range resultsCh {
		results = append(results, r)
	}
	return results
}

// runOne runs a single adapter against a single project. It never lets the
// calling goroutine crash: a panic inside CheckInstalled/Install/Run is
// recovered and reported as a skipped result carrying the panic value and
// stack trace, so one broken adapter can never take down the rest of the run.
func runOne(a adapter.ToolAdapter, p detect.Project, inst *installer, c Cache) (result ToolResult) {
	defer func() {
		if r := recover(); r != nil {
			// Path must be set here too. It was missing on this branch once
			// before and a panicking adapter rendered as "skipped gosec ():
			// panic..." with an empty path in the JSON report; a review
			// caught it. Do not drop it while moving the parameter list.
			result = ToolResult{Tool: a.Name(), Path: p.Path, Skipped: true, Error: fmt.Errorf("panic: %v\n%s", r, debug.Stack())}
		}
	}()

	if err := inst.ensure(a); err != nil {
		return ToolResult{Tool: a.Name(), Path: p.Path, Skipped: true, Error: err}
	}

	targeted, canTarget := a.(adapter.Targeted)
	if c == nil || !canTarget {
		return runFull(a, p)
	}

	stale, cached, ok := c.Lookup(a.Name(), p)
	if !ok {
		return runFull(a, p)
	}
	if len(stale) == 0 {
		// Everything this tool would look at is unchanged since the last
		// run; the tool is not invoked at all.
		return ToolResult{Tool: a.Name(), Path: p.Path, Findings: cached}
	}

	fresh, err := targeted.RunTargets(p.Path, stale)
	if err != nil {
		return ToolResult{Tool: a.Name(), Path: p.Path, Skipped: true, Error: err}
	}
	normalizeFilePaths(fresh, p.Path)
	c.Store(a.Name(), p, stale, fresh)
	return ToolResult{Tool: a.Name(), Path: p.Path, Findings: append(cached, fresh...)}
}

func runFull(a adapter.ToolAdapter, p detect.Project) ToolResult {
	findings, err := a.Run(p.Path)
	if err != nil {
		return ToolResult{Tool: a.Name(), Path: p.Path, Skipped: true, Error: err}
	}
	normalizeFilePaths(findings, p.Path)
	return ToolResult{Tool: a.Name(), Path: p.Path, Findings: findings}
}

// normalizeFilePaths rewrites each finding's File to be relative to the
// project root it was found under, in place. Adapters disagree on this:
// golangci-lint already reports paths relative to the dir it ran in, gosec
// reports absolute paths - so the same file shows up under two different
// names in one report unless it's normalized at this single choke point
// (every adapter's findings pass through runOne). Paths that are already
// relative, empty (cargo-audit's synthetic "Cargo.lock"), or that fail to
// resolve relative to root are left untouched rather than mangled.
//
// root itself may be relative (e.g. `analyser run testdata/fixtures/go-repo`
// from the repo root) while an adapter's File is always absolute (gosec
// resolves ./... against its own working directory) - filepath.Rel errors
// if exactly one of its two arguments is absolute, so root is resolved to
// an absolute path first.
//
// When a path escapes the root, filepath.Rel succeeds but returns a
// traversal chain (../../...). To keep those paths readable, we detect
// escape (first component is "..") and leave the absolute path unchanged.
func normalizeFilePaths(findings []finding.Finding, root string) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return
	}
	for i, f := range findings {
		if f.File == "" || !filepath.IsAbs(f.File) {
			continue
		}
		if rel, err := filepath.Rel(absRoot, f.File); err == nil {
			// Keep absolute if path escapes the root (first component is "..").
			// A filename can legitimately start with ".." (e.g., ..hidden.go),
			// so check for ".." as a path component, not a string prefix.
			sep := string(filepath.Separator)
			if rel != ".." && !strings.HasPrefix(rel, ".."+sep) {
				findings[i].File = rel
			}
		}
	}
}

// installer memoizes install-or-check-installed per tool name so that, when
// multiple projects share a language, a tool's CheckInstalled/Install pair
// runs exactly once per Run call rather than once per project. Every
// goroutine waiting on the same tool observes the same outcome (nil, or the
// same install error).
type installer struct {
	mu   sync.Mutex
	once map[string]*sync.Once
	errs map[string]error
}

func newInstaller() *installer {
	return &installer{once: map[string]*sync.Once{}, errs: map[string]error{}}
}

func (in *installer) ensure(a adapter.ToolAdapter) error {
	name := a.Name()

	in.mu.Lock()
	once, ok := in.once[name]
	if !ok {
		once = &sync.Once{}
		in.once[name] = once
	}
	in.mu.Unlock()

	once.Do(func() {
		if a.CheckInstalled() {
			return
		}
		fmt.Fprintf(os.Stderr, "installing %s...\n", name)
		if err := a.Install(); err != nil {
			in.mu.Lock()
			in.errs[name] = fmt.Errorf("install failed: %w", err)
			in.mu.Unlock()
		}
	})

	in.mu.Lock()
	defer in.mu.Unlock()
	return in.errs[name]
}
