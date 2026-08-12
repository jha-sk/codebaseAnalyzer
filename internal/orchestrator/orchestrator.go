package orchestrator

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"

	"codebase-analyser/internal/adapter"
	"codebase-analyser/internal/detect"
	"codebase-analyser/internal/finding"
)

type ToolResult struct {
	Tool     string
	Findings []finding.Finding
	Skipped  bool
	Error    error
}

var DefaultAdapters = map[string][]adapter.ToolAdapter{
	"go":   {adapter.GolangciLint{}, adapter.Gosec{}, adapter.Govulncheck{}},
	"rust": {adapter.Clippy{}, adapter.CargoAudit{}},
}

// Run executes every adapter for every detected project concurrently. Each
// adapter's timeout is enforced inside its own Run call (see adapter.DefaultTimeout);
// a crashing or erroring adapter is recorded as skipped and never blocks the others.
func Run(projects []detect.Project, adaptersByLang map[string][]adapter.ToolAdapter) []ToolResult {
	var wg sync.WaitGroup
	resultsCh := make(chan ToolResult)
	inst := newInstaller()

	for _, p := range projects {
		for _, a := range adaptersByLang[p.Language] {
			wg.Add(1)
			go func(a adapter.ToolAdapter, path string) {
				defer wg.Done()
				resultsCh <- runOne(a, path, inst)
			}(a, p.Path)
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

// runOne runs a single adapter against a single project path. It never lets
// the calling goroutine crash: a panic inside CheckInstalled/Install/Run
// (e.g. a bug parsing malformed tool output) is recovered and reported as a
// skipped result carrying the panic value and stack trace, so one broken
// adapter can never take down the rest of the run.
func runOne(a adapter.ToolAdapter, path string, inst *installer) (result ToolResult) {
	defer func() {
		if r := recover(); r != nil {
			result = ToolResult{Tool: a.Name(), Skipped: true, Error: fmt.Errorf("panic: %v\n%s", r, debug.Stack())}
		}
	}()

	if err := inst.ensure(a); err != nil {
		return ToolResult{Tool: a.Name(), Skipped: true, Error: err}
	}
	findings, err := a.Run(path)
	if err != nil {
		return ToolResult{Tool: a.Name(), Skipped: true, Error: err}
	}
	return ToolResult{Tool: a.Name(), Findings: findings}
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
