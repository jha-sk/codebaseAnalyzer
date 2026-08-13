# JS/TS Language Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the analyser beyond Go and Rust so a JS/TS repo — including a workspace monorepo — is detected, linted, type-checked and dependency-audited through the pipeline that already exists.

**Architecture:** Three new `ToolAdapter` implementations (`ESLint`, `Tsc`, `JSAudit`) plus one shared plumbing file that owns the pinned Node tool cache, and a widened `detect.Detect`. Nothing in the pipeline, the `Finding` schema, the categories, the report renderers or the MCP server changes shape — the new languages are two more keys in `orchestrator.DefaultAdapters`. Every JS/TS tool is invoked from the analyser's own version-pinned install under the user cache dir, except the dependency audit, which by definition must use the repo's own package manager.

**Tech Stack:** Go 1.26.5, stdlib only (no new Go module dependencies). Node tooling pinned at ESLint 9.39.0, TypeScript 5.9.3, typescript-eslint 8.46.0, eslint-plugin-promise 7.2.1, eslint-plugin-security 3.0.1, globals 16.5.0, installed via `npm install --prefix`.

**Spec:** [docs/superpowers/specs/2026-08-13-jsts-language-support-design.md](../specs/2026-08-13-jsts-language-support-design.md)

---

## Deviation from the spec, and why

The spec's Toolchain section asks for a new implementation of the resolver interface that "downloads/caches an isolated Node install — never touching the system Node, same as the Go/Rust resolvers".

`internal/toolchain` now exists (landed by the MCP-server session while this plan was being written): `toolchain.Resolver` is `Detect(repoPath) (version, ok)` + `Ensure(version) (env, error)`, wired into tool runs through the `adapter.EnvForPath` hook in `runCommand`. So the interface the spec refers to is real, and adding a language to it is genuinely not a rework.

**But neither existing resolver downloads anything.** `Go.Ensure` returns `GOTOOLCHAIN=go1.x`, delegating to Go's own signed toolchain switching; `Rust.Detect` is still a stub that returns `("", false)`, and `Rust.Ensure` returns nil. The pattern is *lean on the language's own version switcher*, and **Node has no equivalent** — no environment variable makes an installed `node` become a different `node`. A Node resolver that honoured `.nvmrc` would have to fetch, verify and unpack a platform-specific tarball, which is a subsystem neither shipped language needed.

**What this plan does instead:** the analyser uses the system `node`/`npm` to install its own **version-pinned analysis tools** into a private cache directory (Task 2) — reproducible results, no `npx`-per-run, no global installs, the repo's own `node_modules` untouched. A machine with no Node at all gets one clear "install Node.js" error instead of a silent zero-finding pass.

**Deferred, deliberately:** a `toolchain.Node` resolver pinning the Node *runtime* from `.nvmrc` / `package.json` `engines`. It needs a real downloader, and it plugs into the `EnvForPath` hook that already exists — so it is a clean follow-up task, not a prerequisite.

That downloader is being built right now by the MCP-server session as the last stage of their plan: four unexported, deliberately language-agnostic helpers landing in `internal/toolchain` — `download(url, sumURL)` (streams to a temp file while hashing, refuses to extract on a sha256 mismatch), `extractTarGz`, `safeJoin` (rejects archive entries escaping the destination), and `lock` (serialises concurrent bootstraps behind an `O_EXCL` lock file). They are unexported but in-package, so a `node.go` added to `internal/toolchain` can call all four directly. Once that lands, the deferred work here is `Detect` plus a platform URL template — small enough that building a JS-shaped downloader now would be waste, which is the whole reason it is deferred rather than done.

## Note before you start: `docs/` is not under version control

`.gitignore` line 3 is `/docs`, so this plan, the spec it implements, and every other design document in the tree are untracked. That was a deliberate human choice, so nothing here changes it — but do not assume a plan document survives a clean checkout, and do not treat "it's written down in docs/" as durable.

## Current working-tree state

`internal/adapter/js.go`, `internal/adapter/eslintrules.go`, `internal/adapter/baseline/eslint.config.mjs` and `internal/adapter/baseline/eslintrc.json` were written **before** this plan and are already on disk. Tasks 2 and 3 are therefore verify-and-test tasks: read the existing file, check it against the task's stated contract, and write the tests it does not yet have. If you would rather start clean, delete those four files first and the tasks' contracts fully specify what to re-create.

## Execution: parallel low-cost agents

Dispatch one fresh subagent per task. Use a **low-cost tier** throughout — the code is fully specified below, so the executor is transcribing and testing, not designing. Tier is given in each task header (`haiku` = fully mechanical; `sonnet` = must read existing code and exercise judgement).

**Dependency graph — anything on the same line runs in parallel:**

```
Task 1 ‖ Task 2                (disjoint packages: detect vs adapter plumbing)
Task 3                         (depends on 2 only for living in the same package; may start immediately)
Task 4 ‖ Task 5 ‖ Task 6       (all depend on Task 2's helpers; Task 4 also on Task 3)
Task 7                         (depends on 1, 4, 5, 6 — it wires them together)
Task 8                         (depends on 7 — it runs the wired pipeline)
Task 9                         (depends on 8 — it needs the fixtures and real Node)
```

**Task 9 is not optional and must not be folded into Task 8.** Every parser in Tasks 4–6 was written from a fixture in this document; none has met the real tool. This repo has twice shipped a parser that agreed perfectly with its hand-written fixture and returned zero findings against the real tool. Task 9 is the step where the tool gets consulted.

Two agents must never edit the same file concurrently. Every task above creates its own files; the only shared-file contention is `internal/adapter/js.go` (Task 2 only — Tasks 4/5/6 **read** it and must not modify it) and `internal/detect/detect_test.go` (Task 1 writes it, Task 8 appends to it, and they are strictly ordered).

## Global Constraints

- **Version control is the user's, and every task is a commit checkpoint.** No task runs `git add`, `git commit`, `git init`, or any other mutating git command. A task is done when `go build ./... && go test ./...` is green. Then: report what changed, emit **one single-line commit message** on its own line, and **stop**. The user commits and pushes; the next task does not start until they say so.
- Module path is `codebase-analyser`, Go 1.26.5. Import internal packages as `codebase-analyser/internal/<pkg>`.
- **No new Go dependencies.** `go.mod` must not change. Every parser in this plan is `encoding/json`, `regexp` or a hand-rolled line scan — including the pnpm workspace file, which is read with a small line scanner rather than by adding a YAML library for one flat list.
- `Category` ∈ `correctness | concurrency | security | operational`; `Severity` ∈ `critical | high | medium | low`. New findings are constructed with the `finding.Category*` / `finding.Severity*` constants, never with string literals.
- **A tool with nothing to do returns `(nil, nil)`, never an error.** `tsc` in a repo with no `tsconfig.json`, and `js-audit` in a package with no lockfile, are normal states — not skipped tools. The orchestrator turns any error into a skipped-tool note, and `computeExitCode` turns any skipped tool into exit code 2. Reporting "coverage was incomplete" on a JS-only repo, on every run, would make that signal worthless.
- **Adapters never normalize file paths.** `orchestrator.normalizeFilePaths` is the single choke point that rewrites absolute paths to project-relative ones. Emit whatever the tool printed.
- **`runCommand` already handles linter exit codes.** A non-zero exit *with* stdout is success; a non-zero exit with no stdout is a real failure carrying the exit code and truncated stderr. Do not add exit-code handling in an adapter.
- **None of the three new adapters implements `adapter.Targeted`.** ESLint, tsc and a dependency audit have no sub-unit matching the `cache.Unit` model, so `orchestrator.RunWithCache` will always take the `runFull` path for them. Do not add `RunTargets` methods.
- Excluded from every JS/TS scan: `node_modules`, `dist`, `build`, `.next`, `out`, `coverage` (spec: Excluded paths). The list lives once, in `jsExcludedDirs` in `internal/adapter/js.go`, and `internal/detect` skips the same names.
- **A repo's own ESLint config always wins** (spec: No-config repos). The analyser's baseline is applied only when the repo has no config of any kind.
- Reuse before writing: `runCommand`, `commandExists`, `resolveCommand`, `finding.Finding`, `orchestrator.DefaultAdapters`, and the `js.go` helpers listed in Task 2's Produces block.

---

## File Structure

```
codebase-analyser/
├── internal/
│   ├── detect/
│   │   ├── detect.go                  # + package.json -> "js"/"ts", + skip dirs   [Task 1]
│   │   └── detect_test.go             #                                    [Tasks 1, 8]
│   ├── adapter/
│   │   ├── js.go                      # pinned tool cache, jsBin, findUp, excludes [Task 2]
│   │   ├── js_test.go                 #                                            [Task 2]
│   │   ├── baseline/
│   │   │   ├── eslint.config.mjs      # flat baseline, go:embed'd                  [Task 2]
│   │   │   └── eslintrc.json          # legacy baseline, go:embed'd                [Task 2]
│   │   ├── eslintrules.go             # ruleID -> {category, severity} table       [Task 3]
│   │   ├── eslintrules_test.go        #                                            [Task 3]
│   │   ├── eslint.go / eslint_test.go # ESLint adapter                             [Task 4]
│   │   ├── tsc.go / tsc_test.go       # tsc --noEmit adapter                       [Task 5]
│   │   └── jsaudit.go / jsaudit_test.go # npm|yarn|pnpm audit adapter              [Task 6]
│   ├── orchestrator/orchestrator.go   # + "js"/"ts" keys in DefaultAdapters        [Task 7]
│   ├── cache/packages.go              # Units: explicit nil for non-Go/Rust        [Task 7]
│   ├── cli/run.go                     # help text + "no project found" message     [Task 7]
│   └── mcpserver/analyze.go           # same "no project found" message            [Task 7]
├── cmd/analyser/main.go               # one-line Short description                 [Task 7]
├── testdata/fixtures/js-repo|ts-repo|js-monorepo|js-flat-config|js-legacy-config   [Task 8]
└── jsts_e2e_test.go                   # end-to-end smoke, skipped without Node     [Task 8]
```

---
### Task 1: Detect JS/TS projects  *(tier: haiku)*

**Files:**
- Modify: `internal/detect/detect.go`
- Test: `internal/detect/detect_test.go` (append; do not rewrite the existing tests)

**Interfaces:**
- Consumes: nothing from other tasks. This task can start immediately.
- Produces: `detect.Project{Path string, Language string}` where `Language` is now one of `"go" | "rust" | "js" | "ts"`. Task 7 keys `orchestrator.DefaultAdapters` off exactly those two new strings; Task 8's fixtures assert them.

**Context you need:** `detect.Detect(root)` walks the tree once and emits one `Project` per marker file it finds (`go.mod`, `Cargo.toml`). It returns `(projects, skippedPaths, err)` — an unreadable subdirectory is recorded in `skippedPaths` and the walk continues, which is existing behaviour you must not break.

- [ ] **Step 1: Write the failing test for JS and TS detection**

Append to `internal/detect/detect_test.go`:

```go
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
```

Add `"reflect"` and `"sort"` to the test file's import block.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/detect/ -run 'TestDetectJSAndTS|TestDetectSkipsGeneratedDirs|TestDetectMonorepoMembers|TestDetectGoAndJSInSameDir' -v`

Expected: FAIL — `got 0 projects, want 2`, because `package.json` is not yet a marker file.

- [ ] **Step 3: Widen the skip list and add the package.json marker**

Replace the whole body of `internal/detect/detect.go` with:

```go
package detect

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type Project struct {
	Path     string
	Language string // "go", "rust", "js" or "ts"
}

// skipDirs are directory names the walk never descends into: VCS metadata,
// dependency trees, and build output. The JS/TS entries mirror
// adapter.jsExcludedDirs - the two lists are deliberately duplicated rather
// than shared, because detect importing adapter would invert the dependency
// direction the whole pipeline is built on (adapter knows nothing about
// detection, and cache already imports adapter).
//
// Note this widens the skip list for Go and Rust too: a go.mod under
// dist/ or build/ stops being detected. That is the intended reading of
// "generated output is not a project" and matches how testdata/ and
// vendor/ are already treated.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"target":       true,
	"testdata":     true,
	"dist":         true,
	"build":        true,
	".next":        true,
	"out":          true,
	"coverage":     true,
}

// Detect walks root for go.mod / Cargo.toml / package.json, one Project per
// directory found. A repo may contain several languages, and one directory
// may be more than one project (a Go service with a JS build pipeline is
// both). The second return value lists any paths the walk couldn't read
// (e.g. permission-denied subdirectories) as "path: error" strings - the
// walk skips over them and keeps going rather than aborting the whole scan,
// but the caller can still surface that part of the tree wasn't examined
// instead of silently under-reporting.
func Detect(root string) ([]Project, []string, error) {
	var projects []Project
	var skipped []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", path, err))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		dir := filepath.Dir(path)
		switch d.Name() {
		case "go.mod":
			projects = append(projects, Project{Path: dir, Language: "go"})
		case "Cargo.toml":
			projects = append(projects, Project{Path: dir, Language: "rust"})
		case "package.json":
			projects = append(projects, Project{Path: dir, Language: jsLanguage(dir)})
		}
		return nil
	})
	if err != nil {
		return nil, skipped, fmt.Errorf("detect: %w", err)
	}
	return projects, skipped, nil
}

// jsLanguage distinguishes a TypeScript package from a plain JavaScript one
// by the presence of a tsconfig.json beside the package.json. It is the only
// thing that decides whether the tsc adapter runs (spec: Detection).
func jsLanguage(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "tsconfig.json")); err == nil {
		return "ts"
	}
	return "js"
}
```

- [ ] **Step 4: Run the whole detect suite**

Run: `go test ./internal/detect/ -v`

Expected: PASS, including the four pre-existing tests (`TestDetect`, `TestDetectNone`, `TestDetectSkipsTestdataDir`, `TestDetectUnreadableDirSkippedNotFatal`) — the skip-list refactor from a `switch` to a map must not change their behaviour.

- [ ] **Step 5: Report and stop**

`go build ./... && go test ./...` must be green. Then report what changed, emit ONE single-line commit message, and STOP — the user commits. Never run any mutating git command.

---
### Task 2: Pinned Node tool cache and shared JS plumbing  *(tier: sonnet)*

**Files:**
- Verify (already in the working tree): `internal/adapter/js.go`
- Verify (already in the working tree): `internal/adapter/baseline/eslint.config.mjs`, `internal/adapter/baseline/eslintrc.json`
- Test: `internal/adapter/js_test.go`

**Interfaces:**
- Consumes: `commandExists`, `maxStderrInError` from `internal/adapter/adapter.go`.
- Produces — Tasks 4, 5 and 6 call exactly these and must not modify this file:
  - `installJSTools() error` — npm-installs the pinned tool set into the private cache and writes both baseline configs beside it. Guarded by a package-level `sync.Once`, so all three adapters' `Install()` methods share one install.
  - `jsToolsDir() string` — the private cache dir. Honours `CODEBASE_ANALYSER_CACHE`, else `os.UserCacheDir()/codebase-analyser/js-tools`.
  - `pinnedJSBin(name string) string` — path to a tool inside that cache, whether or not it exists.
  - `jsBin(projectPath, name string) string` — the copy to actually run: the repo's own `node_modules/.bin/<name>`, searched from `projectPath` upwards, else the pinned copy, else `""`.
  - `findUp(start, name string) string` — dir at or above `start` containing `name`, else `""`.
  - `isExecutable(path string) bool`
  - `jsExcludedDirs []string` — `node_modules dist build .next out coverage`.
  - `trimForError(b []byte) string` — truncates to `maxStderrInError`.
  - `baselineFlatConfigPath() string`, `baselineLegacyConfigPath() string`.

**Why the repo's own copy wins over the pinned one:** a repo that ships ESLint in its `devDependencies` also ships the plugins and the config that only that copy can resolve. Running our ESLint 9 against a repo's config that imports `eslint-plugin-foo` from *its* `node_modules` fails; running theirs works. The pinned copy is the fallback for repos that have installed nothing — which is also exactly the case where the baseline config applies. The upward search matters for workspaces: a member package has no `node_modules` of its own, the hoisted install lives at the repo root.

**Why pinned versions, not `npx`:** `npx eslint` resolves the latest published version at run time, so the same commit can produce different findings on two different days. Pinning is what makes a run reproducible, and installing once into a shared cache is what keeps repeat runs fast (spec: Toolchain).

- [ ] **Step 1: Read the existing file and check it against the contract above**

Run: `cat internal/adapter/js.go`

Confirm every symbol in the Produces block exists with that exact signature, that `jsPinnedPackages` contains `eslint@9.39.0`, `@eslint/js@9.39.0`, `typescript@5.9.3`, `typescript-eslint@8.46.0`, `eslint-plugin-promise@7.2.1`, `eslint-plugin-security@3.0.1`, `globals@16.5.0`, and that `installJSTools` writes both embedded baselines into `jsToolsDir()` **before** shelling out to npm (so a machine with no npm still fails with the npm error rather than a missing-config error later). Fix anything that does not match. Do not add features.

- [ ] **Step 2: Write the failing tests**

Create `internal/adapter/js_test.go`:

```go
package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestJSToolsDirHonoursCacheOverride: tests must be able to redirect the
// tool cache, and CODEBASE_ANALYSER_CACHE is the seam cache.Root already
// uses for the same reason (os.UserCacheDir honours XDG_CACHE_HOME on Linux
// but has no equivalent on macOS or Windows).
func TestJSToolsDirHonoursCacheOverride(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", "/tmp/analyser-cache-test")
	if got, want := jsToolsDir(), filepath.Join("/tmp/analyser-cache-test", "js-tools"); got != want {
		t.Errorf("jsToolsDir() = %q, want %q", got, want)
	}
}

func TestFindUp(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "packages", "a", "src")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pnpm-lock.yaml"), []byte("lockfileVersion: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := findUp(deep, "pnpm-lock.yaml"); got != root {
		t.Errorf("findUp = %q, want %q", got, root)
	}
	if got := findUp(deep, "no-such-file.json"); got != "" {
		t.Errorf("findUp for a missing file = %q, want \"\" (it must stop at the filesystem root, not loop)", got)
	}
}

func TestIsExecutable(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.txt")
	os.WriteFile(plain, []byte("x"), 0o644)
	exe := filepath.Join(dir, "runme")
	os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755)

	if isExecutable(dir) {
		t.Error("isExecutable(dir) = true, want false")
	}
	if isExecutable(plain) {
		t.Error("isExecutable(non-executable file) = true, want false")
	}
	if !isExecutable(exe) {
		t.Error("isExecutable(executable file) = false, want true")
	}
	if isExecutable(filepath.Join(dir, "nope")) {
		t.Error("isExecutable(missing) = true, want false")
	}
}

// TestJSBinPrefersRepoLocal is the important one: a repo that ships its own
// ESLint must be linted with that copy, because its config and plugins only
// resolve there. The pinned copy is the fallback, not the default.
func TestJSBinPrefersRepoLocal(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("CODEBASE_ANALYSER_CACHE", cache)
	pinned := filepath.Join(cache, "js-tools", "node_modules", ".bin")
	os.MkdirAll(pinned, 0o755)
	os.WriteFile(filepath.Join(pinned, "eslint"), []byte("#!/bin/sh\n"), 0o755)

	repo := t.TempDir()
	member := filepath.Join(repo, "packages", "a")
	os.MkdirAll(member, 0o755)

	// No repo-local copy anywhere: falls back to the pinned one.
	if got, want := jsBin(member, "eslint"), filepath.Join(pinned, "eslint"); got != want {
		t.Errorf("jsBin with no repo-local copy = %q, want the pinned %q", got, want)
	}

	// A hoisted install at the repo root wins for a member package.
	hoisted := filepath.Join(repo, "node_modules", ".bin")
	os.MkdirAll(hoisted, 0o755)
	os.WriteFile(filepath.Join(hoisted, "eslint"), []byte("#!/bin/sh\n"), 0o755)
	if got, want := jsBin(member, "eslint"), filepath.Join(hoisted, "eslint"); got != want {
		t.Errorf("jsBin = %q, want the hoisted repo copy %q", got, want)
	}

	// A copy in the member package itself wins over the hoisted one.
	local := filepath.Join(member, "node_modules", ".bin")
	os.MkdirAll(local, 0o755)
	os.WriteFile(filepath.Join(local, "eslint"), []byte("#!/bin/sh\n"), 0o755)
	if got, want := jsBin(member, "eslint"), filepath.Join(local, "eslint"); got != want {
		t.Errorf("jsBin = %q, want the member-local copy %q", got, want)
	}
}

func TestJSBinMissingEverywhere(t *testing.T) {
	t.Setenv("CODEBASE_ANALYSER_CACHE", t.TempDir())
	if got := jsBin(t.TempDir(), "eslint"); got != "" {
		t.Errorf("jsBin = %q, want \"\" when the tool exists nowhere", got)
	}
}

// TestPinnedPackagesAreVersionPinned guards the reproducibility promise: an
// unpinned entry silently reintroduces "results shift when a tool
// publishes", which is the whole reason this cache exists.
func TestPinnedPackagesAreVersionPinned(t *testing.T) {
	for _, p := range jsPinnedPackages {
		// Strip a leading scope so "@eslint/js@9.39.0" still has its version found.
		at := strings.LastIndex(p, "@")
		if at <= 0 || at == len(p)-1 {
			t.Errorf("package %q is not version-pinned", p)
		}
	}
}

// TestBaselineConfigsEmbedded: the go:embed directives are silent about an
// empty file, and an empty baseline would make every no-config repo report
// zero findings while looking like a clean pass.
func TestBaselineConfigsEmbedded(t *testing.T) {
	if !strings.Contains(string(baselineFlatConfig), "export default") {
		t.Error("flat baseline does not export a config")
	}
	var legacy map[string]any
	if err := json.Unmarshal(baselineLegacyConfig, &legacy); err != nil {
		t.Fatalf("legacy baseline is not valid JSON: %v", err)
	}
	if _, ok := legacy["rules"]; !ok {
		t.Error("legacy baseline has no rules block")
	}
}

func TestJSExcludedDirs(t *testing.T) {
	want := map[string]bool{"node_modules": true, "dist": true, "build": true, ".next": true, "out": true, "coverage": true}
	if len(jsExcludedDirs) != len(want) {
		t.Fatalf("jsExcludedDirs = %v, want %d entries", jsExcludedDirs, len(want))
	}
	for _, d := range jsExcludedDirs {
		if !want[d] {
			t.Errorf("unexpected excluded dir %q", d)
		}
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/adapter/ -run 'TestJS|TestFindUp|TestIsExecutable|TestPinnedPackages|TestBaselineConfigs' -v`

Expected: PASS. If `TestJSToolsDirHonoursCacheOverride` fails, `js.go` is reading `os.UserCacheDir()` unconditionally — add the `CODEBASE_ANALYSER_CACHE` branch. If the embed test fails to compile with `pattern baseline/...: no matching files found`, the two baseline files are missing; re-create them from the contents in Step 4.

- [ ] **Step 4: Verify the baseline configs**

`internal/adapter/baseline/eslint.config.mjs` must import `@eslint/js`, `typescript-eslint`, `eslint-plugin-promise`, `eslint-plugin-security` and `globals`, scope the typescript-eslint recommended set to TS files only (applied to plain `.js` it reports parse noise on valid JavaScript), ignore the six excluded dirs, and leave `security/detect-object-injection` **off** — it fires on every `obj[key]` and would drown the report.

It must NOT enable typescript-eslint's type-aware rules (`no-floating-promises`, `no-misused-promises`). They need a full type-check per lint, which roughly triples runtime and fails outright on repos whose tsconfig does not cover every file. `promise/catch-or-return` is the syntactic approximation that ships instead — the ceiling is recorded in a `ponytail:` comment at the top of the file.

`internal/adapter/baseline/eslintrc.json` is its legacy-format twin, used only when the analyser falls back to a repo-local ESLint 8 that has no config of its own (spec: No-config repos, "authored in both legacy and flat config formats"). Same rules, same ignores, with the TS parser applied through an `overrides` block.

- [ ] **Step 5: Report and stop**

`go build ./... && go test ./...` must be green. Then report what changed, emit ONE single-line commit message, and STOP — the user commits. Never run any mutating git command.

---

### Task 3: ESLint rule → category/severity table  *(tier: haiku)*

**Files:**
- Verify (already in the working tree): `internal/adapter/eslintrules.go`
- Test: `internal/adapter/eslintrules_test.go`

**Interfaces:**
- Consumes: `finding.Category*` / `finding.Severity*` constants.
- Produces: `classifyESLintRule(ruleID string, level int, fatal bool) (finding.Category, finding.Severity)` — Task 4's parser calls exactly this, once per ESLint message.

**Why this exists:** ESLint natively distinguishes only `warn` (1) and `error` (2), which is far coarser than the analyser's four-level scale, and — unlike gosec — it has no inherent sense that a security rule outranks a style nit. Worse, ESLint's plugin ecosystem puts rules from four different analyser categories into a single tool run, so category cannot be a property of the adapter the way it is for gosec (always security) or clippy (mostly correctness). Both fields are therefore assigned by a curated per-rule lookup (spec: Severity & category mapping).

Three layers, in order: exact rule id → plugin prefix (`security/` → security, `promise/` → concurrency) → ESLint's own level, damped to medium/low so an unmapped style rule a repo configured as `error` can never outrank a mapped real bug.

- [ ] **Step 1: Read the existing file and check it against the contract**

Run: `cat internal/adapter/eslintrules.go`

Confirm `classifyESLintRule` has that exact signature, that a `fatal` message or an empty `ruleID` returns `(correctness, high)`, and that the fallback for an unmapped rule is `(correctness, medium)` at level 2 and `(correctness, low)` at level 1.

- [ ] **Step 2: Write the failing tests**

Create `internal/adapter/eslintrules_test.go`:

```go
package adapter

import (
	"testing"

	"codebase-analyser/internal/finding"
)

func TestClassifyESLintRule(t *testing.T) {
	tests := []struct {
		name     string
		ruleID   string
		level    int
		fatal    bool
		category finding.Category
		severity finding.Severity
	}{
		{"eval is critical security", "no-eval", 2, false, finding.CategorySecurity, finding.SeverityCritical},
		{"child_process is high security", "security/detect-child-process", 2, false, finding.CategorySecurity, finding.SeverityHigh},
		{"floating promise is concurrency", "@typescript-eslint/no-floating-promises", 2, false, finding.CategoryConcurrency, finding.SeverityHigh},
		{"catch-or-return is concurrency", "promise/catch-or-return", 2, false, finding.CategoryConcurrency, finding.SeverityHigh},
		{"async promise executor is concurrency", "no-async-promise-executor", 2, false, finding.CategoryConcurrency, finding.SeverityHigh},
		{"undefined variable is correctness", "no-undef", 2, false, finding.CategoryCorrectness, finding.SeverityHigh},
		{"debugger is operational", "no-debugger", 2, false, finding.CategoryOperational, finding.SeverityMedium},
		{"console is operational and low", "no-console", 2, false, finding.CategoryOperational, finding.SeverityLow},

		// Prefix fallback: a rule the table has never heard of still lands in
		// its plugin's category rather than defaulting to correctness.
		{"unknown security rule keeps the category", "security/detect-brand-new-thing", 2, false, finding.CategorySecurity, finding.SeverityMedium},
		{"unknown promise rule keeps the category", "promise/some-future-rule", 1, false, finding.CategoryConcurrency, finding.SeverityLow},

		// Level fallback, damped so an unmapped rule can't outrank a mapped bug.
		{"unmapped error", "unicorn/prefer-node-protocol", 2, false, finding.CategoryCorrectness, finding.SeverityMedium},
		{"unmapped warn", "unicorn/prefer-node-protocol", 1, false, finding.CategoryCorrectness, finding.SeverityLow},
		{"unknown level", "unicorn/prefer-node-protocol", 7, false, finding.CategoryCorrectness, finding.SeverityLow},

		// A parse error means nothing in the file was linted at all.
		{"fatal message", "", 2, true, finding.CategoryCorrectness, finding.SeverityHigh},
		{"null ruleId", "", 2, false, finding.CategoryCorrectness, finding.SeverityHigh},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat, sev := classifyESLintRule(tt.ruleID, tt.level, tt.fatal)
			if cat != tt.category || sev != tt.severity {
				t.Errorf("classifyESLintRule(%q, %d, %v) = (%s, %s), want (%s, %s)",
					tt.ruleID, tt.level, tt.fatal, cat, sev, tt.category, tt.severity)
			}
		})
	}
}

// TestESLintRuleTableIsValid catches typos in the table itself: a category
// or severity string that no longer parses would silently produce findings
// the report can't filter or rank.
func TestESLintRuleTableIsValid(t *testing.T) {
	for ruleID, class := range eslintRuleClasses {
		if ruleID == "" {
			t.Error("table has an empty rule id")
		}
		if _, err := finding.ParseCategory(string(class.Category)); err != nil {
			t.Errorf("rule %q: %v", ruleID, err)
		}
		if _, err := finding.ParseSeverity(string(class.Severity)); err != nil {
			t.Errorf("rule %q: %v", ruleID, err)
		}
	}
	for prefix, cat := range eslintPluginCategories {
		if _, err := finding.ParseCategory(string(cat)); err != nil {
			t.Errorf("prefix %q: %v", prefix, err)
		}
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/adapter/ -run 'TestClassifyESLintRule|TestESLintRuleTableIsValid' -v`

Expected: PASS. A failure here means the table disagrees with the contract — fix the table, not the test, unless the test's expectation is itself wrong (e.g. you deliberately re-graded a rule, in which case change both and say so in the commit message).

- [ ] **Step 4: Report and stop**

`go build ./... && go test ./...` must be green. Then report what changed, emit ONE single-line commit message, and STOP — the user commits. Never run any mutating git command.

---
### Task 4: ESLint adapter  *(tier: sonnet)*

**Files:**
- Create: `internal/adapter/eslint.go`
- Create: `internal/adapter/eslint_test.go`
- Create: `internal/adapter/testdata/eslint_sample.json`

**Interfaces:**
- Consumes: `installJSTools() error`; `jsBin(projectPath, name string) string`; `pinnedJSBin(name string) string`; `baselineFlatConfigPath() string`; `baselineLegacyConfigPath() string`; `jsToolsDir() string`; `jsExcludedDirs []string`; `isExecutable(path string) bool`; `classifyESLintRule(ruleID string, level int, fatal bool) (finding.Category, finding.Severity)` (all in `internal/adapter`); `runCommand(dir, name string, args ...string) ([]byte, error)` (`internal/adapter/adapter.go`); `finding.Finding` (`internal/finding/finding.go`).
- Produces: `adapter.ESLint` (a `ToolAdapter`: `Name() string`, `CheckInstalled() bool`, `Install() error`, `Run(path string) ([]finding.Finding, error)`) for later orchestrator-wiring tasks to add to `orchestrator.DefaultAdapters["js"]`/`["ts"]`. Unexported helpers `eslintFindings(r io.Reader) ([]finding.Finding, error)`, `repoHasESLintConfig(dir string) bool`, `workspaceIgnoreGlobs(dir string) []string`, `eslintMajorVersion(out string) int` — package-internal only, no other task needs to call them directly. `ESLint` deliberately does NOT implement `Targeted`/`RunTargets`: ESLint has no package/crate-sized granularity the way Go packages or Rust crates do (a single `eslint .` invocation walks and lints the whole project tree in one process), so there is nothing smaller to restrict a run to that lines up with `cache.Unit`. Do not add `RunTargets` in this task.

- [ ] **Step 1: Write the failing tests for `eslintFindings`**

Create `internal/adapter/testdata/eslint_sample.json`:

```json
[
  {
    "filePath": "/repo/src/a.js",
    "messages": [
      {"ruleId": "no-eval", "severity": 2, "message": "eval can be harmful.", "line": 12, "column": 3},
      {"ruleId": "no-unused-vars", "severity": 1, "message": "'x' is defined but never used.", "line": 5, "column": 7}
    ],
    "errorCount": 1,
    "warningCount": 1
  },
  {
    "filePath": "/repo/src/b.ts",
    "messages": [
      {"ruleId": null, "fatal": true, "severity": 2, "message": "Parsing error: Unexpected token '}'", "line": 1, "column": 1}
    ],
    "errorCount": 1,
    "warningCount": 0
  },
  {
    "filePath": "/repo/src/c.js",
    "messages": [],
    "errorCount": 0,
    "warningCount": 0
  },
  {
    "filePath": "/repo/src/d.js",
    "messages": [
      {"ruleId": "security/detect-child-process", "severity": 2, "message": "Found require('child_process') call.", "line": 20, "column": 1}
    ],
    "errorCount": 1,
    "warningCount": 0
  }
]
```

Create `internal/adapter/eslint_test.go`:

```go
package adapter

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codebase-analyser/internal/finding"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestESLintFindings_parse(t *testing.T) {
	raw, err := os.ReadFile("testdata/eslint_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := eslintFindings(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 4 {
		t.Fatalf("got %d findings, want 4", len(findings))
	}

	f := findings[0]
	if f.File != "/repo/src/a.js" || f.Line != 12 || f.Tool != "eslint" || f.RuleID != "no-eval" {
		t.Errorf("finding[0] = %+v", f)
	}
	if f.Category != finding.CategorySecurity || f.Severity != finding.SeverityCritical {
		t.Errorf("finding[0] category/severity = %v/%v, want security/critical", f.Category, f.Severity)
	}
	if f.Message != "eval can be harmful." {
		t.Errorf("finding[0].Message = %q", f.Message)
	}

	f = findings[1]
	if f.File != "/repo/src/a.js" || f.Line != 5 || f.RuleID != "no-unused-vars" {
		t.Errorf("finding[1] = %+v", f)
	}
	if f.Category != finding.CategoryCorrectness || f.Severity != finding.SeverityLow {
		t.Errorf("finding[1] category/severity = %v/%v, want correctness/low", f.Category, f.Severity)
	}

	// Fatal parse error (null ruleId): reported under RuleID "fatal", always
	// correctness/high regardless of ESLint's own severity number.
	f = findings[2]
	if f.File != "/repo/src/b.ts" || f.Line != 1 || f.RuleID != "fatal" {
		t.Errorf("finding[2] = %+v, want fatal parse error on b.ts:1", f)
	}
	if f.Category != finding.CategoryCorrectness || f.Severity != finding.SeverityHigh {
		t.Errorf("finding[2] category/severity = %v/%v, want correctness/high", f.Category, f.Severity)
	}
	if !strings.Contains(f.Message, "Parsing error") {
		t.Errorf("finding[2].Message = %q, want it to mention the parse error", f.Message)
	}

	// /repo/src/c.js had an empty "messages" array and must be skipped
	// entirely, not turn into a zero-value Finding.
	for _, f := range findings {
		if f.File == "/repo/src/c.js" {
			t.Errorf("clean file c.js produced a finding: %+v", f)
		}
	}

	f = findings[3]
	if f.File != "/repo/src/d.js" || f.Line != 20 || f.RuleID != "security/detect-child-process" {
		t.Errorf("finding[3] = %+v", f)
	}
	if f.Category != finding.CategorySecurity || f.Severity != finding.SeverityHigh {
		t.Errorf("finding[3] category/severity = %v/%v, want security/high", f.Category, f.Severity)
	}
}

func TestESLintFindings_emptyArray(t *testing.T) {
	findings, err := eslintFindings(strings.NewReader("[]"))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("got %d findings, want 0", len(findings))
	}
}

func TestESLintFindings_malformedJSON(t *testing.T) {
	_, err := eslintFindings(strings.NewReader("{not valid json"))
	if err == nil {
		t.Fatal("want error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "eslint: parsing output:") {
		t.Errorf("err = %q, want it to contain %q", err.Error(), "eslint: parsing output:")
	}
}

var _ = filepath.Join // keep path/filepath imported for steps added later in this file
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/adapter/ -run TestESLintFindings -v`
Expected: FAIL to compile, `undefined: eslintFindings`

- [ ] **Step 3: Implement `ESLint` and `eslintFindings`**

Create `internal/adapter/eslint.go`:

```go
package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"codebase-analyser/internal/finding"
)

// ESLint has no package/crate-sized granularity the way Go packages or Rust
// crates do — a single `eslint .` invocation walks and lints the whole
// project tree in one process, so there is nothing smaller to restrict a run
// to that would line up with cache.Unit. RunTargets is therefore
// deliberately NOT implemented; ESLint always runs as a full-project Run.
type ESLint struct{}

func (ESLint) Name() string { return "eslint" }

func (ESLint) CheckInstalled() bool { return isExecutable(pinnedJSBin("eslint")) }

func (ESLint) Install() error { return installJSTools() }

type eslintMessage struct {
	RuleID   *string `json:"ruleId"`
	Severity int     `json:"severity"`
	Message  string  `json:"message"`
	Line     int     `json:"line"`
	Fatal    bool    `json:"fatal"`
}

type eslintFileResult struct {
	FilePath string          `json:"filePath"`
	Messages []eslintMessage `json:"messages"`
}

// eslintFindings parses ESLint's `-f json` formatter output. A null ruleId
// (ESLint's own shape for a fatal parse/config error) is reported under the
// rule id "fatal" rather than left blank, so it still shows up as a named
// row in a report instead of an empty cell. File is filePath verbatim — the
// orchestrator, not this function, normalises absolute paths to
// project-relative ones.
func eslintFindings(r io.Reader) ([]finding.Finding, error) {
	var results []eslintFileResult
	if err := json.NewDecoder(r).Decode(&results); err != nil {
		return nil, fmt.Errorf("eslint: parsing output: %w", err)
	}

	var findings []finding.Finding
	for _, res := range results {
		if len(res.Messages) == 0 {
			continue
		}
		for _, m := range res.Messages {
			classifyID, displayID := "", "fatal"
			if m.RuleID != nil && *m.RuleID != "" {
				classifyID, displayID = *m.RuleID, *m.RuleID
			}
			category, severity := classifyESLintRule(classifyID, m.Severity, m.Fatal)
			findings = append(findings, finding.Finding{
				File:     res.FilePath,
				Line:     m.Line,
				Tool:     "eslint",
				RuleID:   displayID,
				Category: category,
				Severity: severity,
				Message:  m.Message,
			})
		}
	}
	return findings, nil
}
```

This intentionally leaves `Run` unimplemented (compile error) — the next steps add the remaining unexported helpers, then `Run` last so the whole file compiles once.

Note: `Run` isn't written yet, so the package won't build. That's expected — continue to Step 4 immediately rather than running `go build` here; the first fully-green checkpoint is Step 5's test run once `os`, `os/exec`, `path/filepath`, `strconv` are actually used by the helpers added below. If your editor/toolchain errors on unused imports before then, temporarily trim the `import` block in `eslint.go` to `bytes`, `encoding/json`, `fmt`, `io`, and `codebase-analyser/internal/finding` for this step, and restore the full block in Step 6.

- [ ] **Step 4: Run the eslintFindings tests and confirm green**

Run: `go test ./internal/adapter/ -run TestESLintFindings -v`
Expected: PASS (all three `TestESLintFindings_*` tests)

- [ ] **Step 5: Write the failing test for `repoHasESLintConfig`**

Append to `internal/adapter/eslint_test.go` (replace the trailing `var _ = filepath.Join ...` line with this):

```go
func TestRepoHasESLintConfig(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  bool
	}{
		{
			name: "flat config",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "eslint.config.js"), "export default [];")
			},
			want: true,
		},
		{
			name: "legacy dotfile",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, ".eslintrc.json"), "{}")
			},
			want: true,
		},
		{
			name: "eslintConfig key in package.json",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "package.json"), `{"name":"x","eslintConfig":{"rules":{}}}`)
			},
			want: true,
		},
		{
			name:  "bare dir",
			setup: func(t *testing.T, dir string) {},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)
			if got := repoHasESLintConfig(dir); got != tt.want {
				t.Errorf("repoHasESLintConfig(%q) = %v, want %v", dir, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 6: Run it and watch it fail**

Run: `go test ./internal/adapter/ -run TestRepoHasESLintConfig -v`
Expected: FAIL to compile, `undefined: repoHasESLintConfig`

- [ ] **Step 7: Implement `repoHasESLintConfig`**

Append to `internal/adapter/eslint.go`:

```go
// repoHasESLintConfig reports whether dir has its own ESLint configuration,
// in any of the forms ESLint recognises: flat config, legacy .eslintrc
// dotfile, or an "eslintConfig" key in package.json. If the repo has any of
// these the analyser must never override it with the baseline.
func repoHasESLintConfig(dir string) bool {
	names := []string{
		"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs",
		"eslint.config.ts", "eslint.config.mts", "eslint.config.cts",
		".eslintrc", ".eslintrc.js", ".eslintrc.cjs", ".eslintrc.yaml", ".eslintrc.yml", ".eslintrc.json",
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		ESLintConfig json.RawMessage `json:"eslintConfig"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	return len(pkg.ESLintConfig) > 0 && string(pkg.ESLintConfig) != "null"
}
```

Run: `go test ./internal/adapter/ -run TestRepoHasESLintConfig -v`
Expected: still fails to compile at this point (`os/exec`, `strconv` in the import block are unused until later steps) — that's fine, proceed straight to Step 8; Step 10 makes the file whole again. If your workflow requires green at every step, temporarily drop unused imports from `eslint.go` and restore them in Step 10.

- [ ] **Step 8: Write the failing test for `workspaceIgnoreGlobs`**

Append to `internal/adapter/eslint_test.go`:

```go
func TestWorkspaceIgnoreGlobs(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  []string
	}{
		{
			name: "array form",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "package.json"), `{"name":"root","workspaces":["packages/*","apps/*"]}`)
			},
			want: []string{"packages/*", "apps/*"},
		},
		{
			name: "object form",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "package.json"), `{"name":"root","workspaces":{"packages":["libs/*"]}}`)
			},
			want: []string{"libs/*"},
		},
		{
			name: "pnpm-workspace.yaml",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "pnpm-workspace.yaml"), "packages:\n  - \"apps/*\"\n  - \"packages/*\"\n")
			},
			want: []string{"apps/*", "packages/*"},
		},
		{
			name:  "none",
			setup: func(t *testing.T, dir string) {},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)
			got := workspaceIgnoreGlobs(dir)
			if len(got) != len(tt.want) {
				t.Fatalf("workspaceIgnoreGlobs(%q) = %v, want %v", dir, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("workspaceIgnoreGlobs(%q)[%d] = %q, want %q", dir, i, got[i], tt.want[i])
				}
			}
		})
	}
}
```

- [ ] **Step 9: Run it and watch it fail**

Run: `go test ./internal/adapter/ -run TestWorkspaceIgnoreGlobs -v`
Expected: FAIL to compile, `undefined: workspaceIgnoreGlobs`

- [ ] **Step 10: Implement `workspaceIgnoreGlobs` and `eslintMajorVersion`, restore full imports**

Replace the `import` block at the top of `internal/adapter/eslint.go` with:

```go
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"codebase-analyser/internal/finding"
)
```

Append to `internal/adapter/eslint.go`:

```go
// workspaceIgnoreGlobs returns the workspace member globs declared by dir's
// package.json ("workspaces", either the plain array form or the
// {"packages": [...]} object form npm also accepts) and/or its
// pnpm-workspace.yaml, or nil if dir declares no workspace. Each member
// package is detected as its own project and linted on its own, so linting
// it again from the root would report every finding twice under two
// different relative paths — hence these become --ignore-pattern globs.
func workspaceIgnoreGlobs(dir string) []string {
	var globs []string

	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if json.Unmarshal(data, &pkg) == nil && len(pkg.Workspaces) > 0 {
			var list []string
			if json.Unmarshal(pkg.Workspaces, &list) == nil {
				globs = append(globs, list...)
			} else {
				var obj struct {
					Packages []string `json:"packages"`
				}
				if json.Unmarshal(pkg.Workspaces, &obj) == nil {
					globs = append(globs, obj.Packages...)
				}
			}
		}
	}

	if data, err := os.ReadFile(filepath.Join(dir, "pnpm-workspace.yaml")); err == nil {
		globs = append(globs, pnpmWorkspaceGlobs(data)...)
	}

	if len(globs) == 0 {
		return nil
	}
	return globs
}

// pnpmWorkspaceGlobs reads the flat `packages:` list a pnpm-workspace.yaml
// declares, e.g.:
//
//	packages:
//	  - "apps/*"
//	  - "packages/*"
//
// A tiny line scanner rather than a YAML dependency: pnpm-workspace.yaml in
// practice is exactly this one flat list, nothing the analyser needs a full
// parser for.
func pnpmWorkspaceGlobs(data []byte) []string {
	var globs []string
	inPackages := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "packages:":
			inPackages = true
		case inPackages && strings.HasPrefix(trimmed, "- "):
			glob := strings.Trim(strings.TrimPrefix(trimmed, "- "), `"'`)
			if glob != "" {
				globs = append(globs, glob)
			}
		case inPackages && trimmed != "" && !strings.HasPrefix(trimmed, "-"):
			inPackages = false
		}
	}
	return globs
}

// eslintMajorVersion parses the major version out of `eslint --version`
// output (e.g. "v9.39.0"), or 0 if it can't be parsed.
func eslintMajorVersion(out string) int {
	s := strings.TrimPrefix(strings.TrimSpace(out), "v")
	parts := strings.SplitN(s, ".", 2)
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return n
}
```

Run: `go test ./internal/adapter/ -run 'TestESLintFindings|TestRepoHasESLintConfig|TestWorkspaceIgnoreGlobs' -v`
Expected: still fails to compile — `os/exec` is imported but not yet used (`Run` isn't written until Step 13). Continue to Step 11.

- [ ] **Step 11: Write the failing test for `eslintMajorVersion`**

Append to `internal/adapter/eslint_test.go`:

```go
func TestESLintMajorVersion(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"v9.39.0", 9},
		{"8.57.1", 8},
		{"garbage", 0},
	}
	for _, tt := range tests {
		if got := eslintMajorVersion(tt.in); got != tt.want {
			t.Errorf("eslintMajorVersion(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
```

- [ ] **Step 12: Run it and watch it fail**

Run: `go test ./internal/adapter/ -run TestESLintMajorVersion -v`
Expected: FAIL to compile, `imported and not used: "os/exec"` (from `eslint.go`) — this confirms `eslintMajorVersion` itself is already correct and reachable; the package just isn't whole yet because `Run` (the only caller of `os/exec`) doesn't exist. Proceed to Step 13.

- [ ] **Step 13: Implement `Run`, completing the file**

Append to `internal/adapter/eslint.go`:

```go
func (ESLint) Run(path string) ([]finding.Finding, error) {
	bin := jsBin(path, "eslint")
	if bin == "" {
		return nil, fmt.Errorf("eslint not available")
	}

	verOut, _ := exec.Command(bin, "--version").Output()
	major := eslintMajorVersion(string(verOut))

	args := []string{"-f", "json"}

	if !repoHasESLintConfig(path) {
		// No config of the repo's own: apply the analyser's baseline.
		// Format depends on the resolved binary's major version — v9
		// dropped .eslintrc support entirely, v8 doesn't understand flat
		// config. If the version can't be determined, assume flat: our
		// pinned copy of ESLint is 9.x.
		if major != 0 && major <= 8 {
			args = append(args, "--no-eslintrc", "--config", baselineLegacyConfigPath(), "--resolve-plugins-relative-to", jsToolsDir())
		} else {
			args = append(args, "--no-config-lookup", "--config", baselineFlatConfigPath())
		}
	}
	// v9 takes its file globs from the config itself and rejects --ext
	// outright; v8 has no other way to know which extensions to lint.
	if major != 0 && major <= 8 {
		args = append(args, "--ext", ".js,.jsx,.mjs,.cjs,.ts,.tsx,.mts,.cts")
	}

	for _, dir := range jsExcludedDirs {
		args = append(args, "--ignore-pattern", dir+"/**")
	}
	// Workspace member packages are detected as their own projects and
	// linted on their own by the orchestrator, so linting them again from
	// the root would report every finding twice under two different
	// relative paths.
	for _, glob := range workspaceIgnoreGlobs(path) {
		args = append(args, "--ignore-pattern", glob+"/**")
	}

	args = append(args, ".")

	// ESLint exits 1 when it reports problems; runCommand already treats a
	// non-zero exit with stdout present as success, not a run failure.
	out, err := runCommand(path, bin, args...)
	if err != nil {
		return nil, fmt.Errorf("eslint: %w", err)
	}
	return eslintFindings(bytes.NewReader(out))
}
```

Also delete the placeholder line `var _ = filepath.Join // keep path/filepath imported for steps added later in this file` from `internal/adapter/eslint_test.go` if it is still present — `filepath` is now used by the real tests added in Steps 5 and 8.

- [ ] **Step 14: Run the full adapter package test suite**

Run: `go test ./internal/adapter/ -v`
Expected: PASS — every `TestESLint*`, `TestRepoHasESLintConfig`, `TestWorkspaceIgnoreGlobs` case green, and no regressions in the existing `TestClippy*`, `TestGosec*`, etc.

- [ ] **Final step: Report and stop**
`go build ./... && go test ./...` must be green. Then report what changed, emit ONE single-line commit message, and STOP — the user commits. Never run any mutating git command.
### Task 5: tsc adapter  *(tier: haiku)*

**Files:**
- Create: `internal/adapter/tsc.go`
- Test: `internal/adapter/tsc_test.go`

**Interfaces:**
- Consumes: `func installJSTools() error`, `func jsBin(projectPath, name string) string`, `func pinnedJSBin(name string) string`, `func isExecutable(path string) bool` (all from `internal/adapter/js.go`); `func runCommand(dir, name string, args ...string) ([]byte, error)` (from `internal/adapter/adapter.go`); `finding.Finding`, `finding.CategoryCorrectness`, `finding.SeverityHigh`, `finding.SeverityLow` (from `internal/finding/finding.go`).
- Produces: `adapter.Tsc` (implements `adapter.ToolAdapter` — `Name() string`, `CheckInstalled() bool`, `Install() error`, `Run(path string) ([]finding.Finding, error)`), and `func tscFindings(out string) []finding.Finding` for later tasks/tests to call directly.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/tsc_test.go`:

```go
package adapter

import (
	"testing"

	"codebase-analyser/internal/finding"
)

func TestTscFindings(t *testing.T) {
	out := `src/index.ts(12,7): error TS2322: Type 'string' is not assignable to type 'number'.
src/api/client.ts(48,15): error TS2532: Object is possibly 'undefined'.

Found 2 errors in 2 files.
error TS18003: No inputs were found in config file '/repo/tsconfig.json'.
`
	findings := tscFindings(out)
	if len(findings) != 3 {
		t.Fatalf("got %d findings, want 3: %+v", len(findings), findings)
	}

	f0 := findings[0]
	if f0.File != "src/index.ts" || f0.Line != 12 || f0.Tool != "tsc" || f0.RuleID != "TS2322" ||
		f0.Category != finding.CategoryCorrectness || f0.Severity != finding.SeverityHigh ||
		f0.Message != "Type 'string' is not assignable to type 'number'." {
		t.Errorf("findings[0] = %+v", f0)
	}

	f1 := findings[1]
	if f1.File != "src/api/client.ts" || f1.Line != 48 || f1.Tool != "tsc" || f1.RuleID != "TS2532" ||
		f1.Category != finding.CategoryCorrectness || f1.Severity != finding.SeverityHigh ||
		f1.Message != "Object is possibly 'undefined'." {
		t.Errorf("findings[1] = %+v", f1)
	}

	// TS18003 has no file prefix - it must still surface as a finding
	// (attributed to tsconfig.json) rather than being silently dropped,
	// otherwise a broken project configuration reports as clean.
	f2 := findings[2]
	if f2.File != "tsconfig.json" || f2.Line != 0 || f2.Tool != "tsc" || f2.RuleID != "TS18003" ||
		f2.Category != finding.CategoryCorrectness || f2.Severity != finding.SeverityHigh ||
		f2.Message != "No inputs were found in config file '/repo/tsconfig.json'." {
		t.Errorf("findings[2] = %+v", f2)
	}

	// "Found 2 errors in 2 files." and the blank line above must both be
	// skipped silently, not miscounted as findings or cause a panic.
}

func TestTscFindings_WindowsPath(t *testing.T) {
	// The naive regex `(.+)\((\d+),(\d+)\)` must not choke on the colon in
	// the "C:" drive letter - there is nothing colon-based in the regex to
	// confuse, since (.+) matches up to the LAST "(line,col)" on the line.
	out := `C:\repo\src\a.ts(3,1): error TS1005: ';' expected.`
	findings := tscFindings(out)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.File != `C:\repo\src\a.ts` || f.Line != 3 || f.RuleID != "TS1005" || f.Message != "';' expected." {
		t.Errorf("finding = %+v", f)
	}
}

func TestTscFindings_Empty(t *testing.T) {
	if findings := tscFindings(""); findings != nil {
		t.Errorf("tscFindings(\"\") = %+v, want nil", findings)
	}
}

func TestTscFindings_MessageSeverity(t *testing.T) {
	out := `message TS6194: Found 1 error.`
	findings := tscFindings(out)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Severity != finding.SeverityLow {
		t.Errorf("Severity = %v, want low", findings[0].Severity)
	}
}

func TestTscRun_NoTsconfig(t *testing.T) {
	// No tsconfig.json in the temp dir means there is nothing for tsc to
	// type-check. This must return (nil, nil), NOT an error - an error here
	// would make the orchestrator report tsc as a skipped tool and degrade
	// the run's exit code to 2 for a project that was never TypeScript.
	findings, err := (Tsc{}).Run(t.TempDir())
	if findings != nil {
		t.Errorf("findings = %+v, want nil", findings)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/adapter/ -run TestTsc -v`
Expected: FAIL to compile — `undefined: tscFindings` and `undefined: Tsc`.

- [ ] **Step 3: Implement `internal/adapter/tsc.go`**

```go
package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"codebase-analyser/internal/finding"
)

// Tsc wraps `tsc --noEmit`, the TypeScript compiler running in
// type-check-only mode. It shares the pinned Node/TypeScript install with
// the other JS/TS adapters (see js.go).
type Tsc struct{}

func (Tsc) Name() string         { return "tsc" }
func (Tsc) CheckInstalled() bool { return isExecutable(pinnedJSBin("tsc")) }
func (Tsc) Install() error       { return installJSTools() }

// Run type-checks the whole program rooted at path.
//
// Targeted is deliberately not implemented on Tsc: tsc type-checks a whole
// program (it follows imports wherever they lead) and has no meaningful
// notion of "just this subdirectory" to restrict a run to.
func (Tsc) Run(path string) ([]finding.Finding, error) {
	// tsc is only meaningful against a tsconfig.json - without one there is
	// nothing to type-check, and that is not an error: returning an error
	// here would make the orchestrator report tsc as a *skipped* tool and
	// degrade the run's exit code to 2, when "this isn't a TypeScript
	// project" is a perfectly healthy, non-error outcome.
	if _, err := os.Stat(filepath.Join(path, "tsconfig.json")); err != nil {
		return nil, nil
	}
	bin := jsBin(path, "tsc")
	if bin == "" {
		return nil, fmt.Errorf("tsc: not available")
	}
	// tsc has no JSON output mode, so the plain diagnostic format parsed by
	// tscFindings below is all that's available. --pretty false strips the
	// ANSI colour codes and multi-line framing tsc's default "pretty" output
	// uses, which would otherwise make each diagnostic unparseable as a
	// single line.
	//
	// tsc exits non-zero whenever it reports type errors; runCommand already
	// tolerates that (a non-zero exit WITH stdout is treated as success). A
	// tsconfig.json broken enough that tsc dies before emitting any
	// diagnostics produces a real runCommand error here, which is correct:
	// it should surface as a skipped tool with a reason, not silently as
	// zero findings.
	out, err := runCommand(path, bin, "--noEmit", "--pretty", "false")
	if err != nil {
		return nil, fmt.Errorf("tsc: %w", err)
	}
	return tscFindings(string(out)), nil
}

// tscFileDiagLine matches a file-scoped diagnostic, e.g.:
//
//	src/index.ts(12,7): error TS2322: Type 'string' is not assignable to type 'number'.
//	C:\repo\src\a.ts(3,1): error TS1005: ';' expected.
//
// (.+) is greedy, so it matches up to the LAST "(line,col)" on the line -
// that is what keeps a Windows drive-letter colon (C:\...) from being
// mistaken for a field separator: there is nothing colon-based in this
// regex to split on in the first place.
var tscFileDiagLine = regexp.MustCompile(`^(.+)\((\d+),(\d+)\): (error|message|suggestion) (TS\d+): (.*)$`)

// tscProjectDiagLine matches a project-level diagnostic with no file prefix,
// e.g.:
//
//	error TS18003: No inputs were found in config file '/repo/tsconfig.json'.
var tscProjectDiagLine = regexp.MustCompile(`^(error|message|suggestion) (TS\d+): (.*)$`)

// tscSeverity maps a tsc diagnostic level to a Finding severity. tsc has no
// "warning" level in this output, so none is invented here - only "error"
// (high) and "message"/"suggestion" (low) occur.
func tscSeverity(level string) finding.Severity {
	if level == "error" {
		return finding.SeverityHigh
	}
	return finding.SeverityLow
}

// tscFindings parses the plain-text output of `tsc --noEmit --pretty false`.
// tsc interleaves progress and summary lines with its diagnostics; any line
// matching neither diagnostic shape is skipped silently.
func tscFindings(out string) []finding.Finding {
	var findings []finding.Finding
	for _, line := range strings.Split(out, "\n") {
		if m := tscFileDiagLine.FindStringSubmatch(line); m != nil {
			lineNum, _ := strconv.Atoi(m[2])
			findings = append(findings, finding.Finding{
				File:     m[1],
				Line:     lineNum,
				Tool:     "tsc",
				RuleID:   m[5],
				Category: finding.CategoryCorrectness,
				Severity: tscSeverity(m[4]),
				Message:  m[6],
			})
			continue
		}
		// A project-level diagnostic (e.g. TS18003, a broken tsconfig.json)
		// has no file to attach to. It still becomes a finding - attributed
		// to "tsconfig.json" at line 0 - so a misconfigured project shows up
		// instead of silently reporting clean.
		if m := tscProjectDiagLine.FindStringSubmatch(line); m != nil {
			findings = append(findings, finding.Finding{
				File:     "tsconfig.json",
				Line:     0,
				Tool:     "tsc",
				RuleID:   m[2],
				Category: finding.CategoryCorrectness,
				Severity: tscSeverity(m[1]),
				Message:  m[3],
			})
		}
	}
	return findings
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapter/ -run TestTsc -v`
Expected: PASS — `TestTscFindings`, `TestTscFindings_WindowsPath`, `TestTscFindings_Empty`, `TestTscFindings_MessageSeverity`, `TestTscRun_NoTsconfig` all green.

- [ ] **Final step: Report and stop**

`go build ./... && go test ./...` must be green. Then report what changed, emit ONE single-line commit message, and STOP — the user commits. Never run any mutating git command.
### Task 6: JS/TS dependency audit adapter  *(tier: sonnet)*

**Files:**
- Create: `internal/adapter/jsaudit.go`
- Create: `internal/adapter/jsaudit_test.go`
- Create: `internal/adapter/testdata/npmaudit_sample.json`
- Create: `internal/adapter/testdata/yarnaudit_sample.ndjson`
- Create: `internal/adapter/testdata/pnpmaudit_sample.json`
- Create: `internal/adapter/testdata/pnpmaudit_two_sample.json`

**Interfaces:**
- Consumes: `commandExists(name string) bool` and `runCommand(dir, name string, args ...string) ([]byte, error)` (`internal/adapter/adapter.go`); `finding.Finding`, `finding.CategorySecurity`, `finding.SeverityCritical/High/Medium/Low` (`internal/finding/finding.go`). Does **not** call `installJSTools()` or `findUp()` from `internal/adapter/js.go` — see Step 4 comments for why.
- Produces: `adapter.JSAudit{}` (implements `adapter.ToolAdapter`: `Name() string`, `CheckInstalled() bool`, `Install() error`, `Run(path string) ([]finding.Finding, error)`); unexported `detectLockfile(dir string) (lockfile, manager string)`, `npmAuditFindings([]byte) ([]finding.Finding, error)`, `yarnAuditFindings(io.Reader) ([]finding.Finding, error)`, `pnpmAuditFindings([]byte) ([]finding.Finding, error)`, `jsSeverity(string) finding.Severity` — later orchestrator wiring registers `adapter.JSAudit{}` alongside the other JS/TS adapters for the `"js"`/`"ts"` languages, the same way `orchestrator.DefaultAdapters` already registers `CargoAudit{}` for `"rust"`.

- [ ] **Step 1: Create the testdata fixtures**

Create `internal/adapter/testdata/npmaudit_sample.json` (derived from a real `npm audit --json` capture: `minimist`'s `via` holds one real advisory object, `tough-cookie`'s `via` holds only the bare string `"minimist"` — a transitive link with no advisory of its own):

```json
{
  "vulnerabilities": {
    "minimist": {
      "name": "minimist",
      "severity": "critical",
      "via": [
        {
          "source": 1179,
          "name": "minimist",
          "title": "Prototype Pollution in minimist",
          "url": "https://github.com/advisories/GHSA-xvch-5gv4-984h",
          "severity": "critical",
          "cwe": ["CWE-1321"]
        }
      ],
      "range": "<0.2.1"
    },
    "tough-cookie": {
      "name": "tough-cookie",
      "severity": "moderate",
      "via": ["minimist"],
      "range": "*"
    }
  },
  "metadata": {
    "vulnerabilities": {
      "critical": 1,
      "moderate": 1,
      "total": 2
    }
  }
}
```

Create `internal/adapter/testdata/yarnaudit_sample.ndjson` (derived from a real `yarn audit --json` capture; the `auditSummary` line at the end must contribute zero findings):

```
{"type":"auditAdvisory","data":{"resolution":{"id":1179,"path":"minimist"},"advisory":{"module_name":"minimist","severity":"critical","title":"Prototype Pollution","github_advisory_id":"GHSA-xvch-5gv4-984h","cwe":"CWE-1321"}}}
{"type":"auditSummary","data":{"vulnerabilities":{"critical":1}}}
```

Create `internal/adapter/testdata/pnpmaudit_sample.json` (derived from a real `pnpm audit --json` capture, legacy npm-v6 shape, `advisories` keyed by numeric-string id):

```json
{
  "advisories": {
    "1179": {
      "module_name": "minimist",
      "severity": "critical",
      "title": "Prototype Pollution",
      "github_advisory_id": "GHSA-xvch-5gv4-984h",
      "cwe": "CWE-1321",
      "findings": [{ "version": "0.0.8", "paths": ["a>b>minimist"] }]
    }
  },
  "metadata": {
    "vulnerabilities": { "critical": 1 }
  }
}
```

Create `internal/adapter/testdata/pnpmaudit_two_sample.json` (two advisories, ids `"1179"` and `"220"`, to exercise sorted-key determinism):

```json
{
  "advisories": {
    "1179": {
      "module_name": "minimist",
      "severity": "critical",
      "title": "Prototype Pollution",
      "github_advisory_id": "GHSA-xvch-5gv4-984h",
      "cwe": "CWE-1321",
      "findings": [{ "version": "0.0.8", "paths": ["a>b>minimist"] }]
    },
    "220": {
      "module_name": "tough-cookie",
      "severity": "moderate",
      "title": "Denial of Service",
      "github_advisory_id": "GHSA-72xf-g2v4-qvf3",
      "cwe": "CWE-400",
      "findings": [{ "version": "2.0.0", "paths": ["a>tough-cookie"] }]
    }
  },
  "metadata": {
    "vulnerabilities": { "critical": 1, "moderate": 1 }
  }
}
```

- [ ] **Step 2: Write the failing test file**

Create `internal/adapter/jsaudit_test.go`:

```go
package adapter

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codebase-analyser/internal/finding"
)

func TestNpmAuditFindings(t *testing.T) {
	raw, err := os.ReadFile("testdata/npmaudit_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := npmAuditFindings(raw)
	if err != nil {
		t.Fatalf("npmAuditFindings: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}

	var advisory, transitive *finding.Finding
	for i := range findings {
		f := &findings[i]
		if f.RuleID != "" {
			advisory = f
		} else {
			transitive = f
		}
	}
	if advisory == nil {
		t.Fatalf("no finding carries an advisory RuleID: %+v", findings)
	}
	if advisory.RuleID != "GHSA-xvch-5gv4-984h" {
		t.Errorf("advisory RuleID = %q, want GHSA-xvch-5gv4-984h", advisory.RuleID)
	}
	if advisory.Severity != finding.SeverityCritical {
		t.Errorf("advisory Severity = %v, want critical", advisory.Severity)
	}
	if advisory.Category != finding.CategorySecurity {
		t.Errorf("advisory Category = %v, want security", advisory.Category)
	}

	if transitive == nil {
		t.Fatalf("no finding for the string-via (transitive) package: %+v", findings)
	}
	if transitive.Severity != finding.SeverityMedium {
		t.Errorf("transitive Severity = %v, want medium (from moderate)", transitive.Severity)
	}
	if !strings.Contains(transitive.Message, "tough-cookie") {
		t.Errorf("transitive Message = %q, want it to name tough-cookie", transitive.Message)
	}
}

func TestNpmAuditFindings_empty(t *testing.T) {
	findings, err := npmAuditFindings([]byte(`{"vulnerabilities":{}}`))
	if err != nil {
		t.Fatalf("npmAuditFindings: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("got %d findings, want 0: %+v", len(findings), findings)
	}
}

func TestYarnAuditFindings(t *testing.T) {
	raw, err := os.ReadFile("testdata/yarnaudit_sample.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := yarnAuditFindings(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("yarnAuditFindings: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1 (auditSummary line must contribute nothing): %+v", len(findings), findings)
	}
	if findings[0].RuleID != "GHSA-xvch-5gv4-984h" {
		t.Errorf("RuleID = %q, want GHSA-xvch-5gv4-984h", findings[0].RuleID)
	}
	if findings[0].Severity != finding.SeverityCritical {
		t.Errorf("Severity = %v, want critical", findings[0].Severity)
	}
}

func TestYarnAuditFindings_toleratesGarbageLines(t *testing.T) {
	input := `{"type":"auditAdvisory","data":{"resolution":{"id":1179,"path":"minimist"},"advisory":{"module_name":"minimist","severity":"critical","title":"Prototype Pollution","github_advisory_id":"GHSA-xvch-5gv4-984h","cwe":"CWE-1321"}}}
not valid json at all

`
	findings, err := yarnAuditFindings(strings.NewReader(input))
	if err != nil {
		t.Fatalf("yarnAuditFindings: %v (a non-JSON line and a trailing blank line must not error the parse)", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
}

func TestPnpmAuditFindings(t *testing.T) {
	raw, err := os.ReadFile("testdata/pnpmaudit_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := pnpmAuditFindings(raw)
	if err != nil {
		t.Fatalf("pnpmAuditFindings: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].RuleID != "GHSA-xvch-5gv4-984h" {
		t.Errorf("RuleID = %q, want GHSA-xvch-5gv4-984h", findings[0].RuleID)
	}
	if findings[0].Severity != finding.SeverityCritical {
		t.Errorf("Severity = %v, want critical", findings[0].Severity)
	}
}

func TestPnpmAuditFindings_sortedDeterministic(t *testing.T) {
	raw, err := os.ReadFile("testdata/pnpmaudit_two_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		findings, err := pnpmAuditFindings(raw)
		if err != nil {
			t.Fatalf("pnpmAuditFindings: %v", err)
		}
		if len(findings) != 2 {
			t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
		}
		if findings[0].RuleID != "GHSA-xvch-5gv4-984h" || findings[1].RuleID != "GHSA-72xf-g2v4-qvf3" {
			t.Fatalf("run %d: order = [%s, %s], want stable [GHSA-xvch-5gv4-984h, GHSA-72xf-g2v4-qvf3]", i, findings[0].RuleID, findings[1].RuleID)
		}
	}
}

func TestJSSeverity(t *testing.T) {
	cases := []struct {
		in   string
		want finding.Severity
	}{
		{"critical", finding.SeverityCritical},
		{"high", finding.SeverityHigh},
		{"moderate", finding.SeverityMedium},
		{"low", finding.SeverityLow},
		{"info", finding.SeverityLow},
		{"unknown-thing", finding.SeverityMedium},
	}
	for _, c := range cases {
		if got := jsSeverity(c.in); got != c.want {
			t.Errorf("jsSeverity(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectLockfile(t *testing.T) {
	t.Run("npm package-lock.json", func(t *testing.T) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "package-lock.json"), "{}")
		lockfile, manager := detectLockfile(dir)
		if lockfile != "package-lock.json" || manager != "npm" {
			t.Errorf("got (%q, %q), want (package-lock.json, npm)", lockfile, manager)
		}
	})
	t.Run("npm npm-shrinkwrap.json", func(t *testing.T) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "npm-shrinkwrap.json"), "{}")
		lockfile, manager := detectLockfile(dir)
		if lockfile != "npm-shrinkwrap.json" || manager != "npm" {
			t.Errorf("got (%q, %q), want (npm-shrinkwrap.json, npm)", lockfile, manager)
		}
	})
	t.Run("yarn yarn.lock", func(t *testing.T) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "yarn.lock"), "")
		lockfile, manager := detectLockfile(dir)
		if lockfile != "yarn.lock" || manager != "yarn" {
			t.Errorf("got (%q, %q), want (yarn.lock, yarn)", lockfile, manager)
		}
	})
	t.Run("pnpm pnpm-lock.yaml", func(t *testing.T) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "pnpm-lock.yaml"), "")
		lockfile, manager := detectLockfile(dir)
		if lockfile != "pnpm-lock.yaml" || manager != "pnpm" {
			t.Errorf("got (%q, %q), want (pnpm-lock.yaml, pnpm)", lockfile, manager)
		}
	})
	t.Run("npm wins over yarn when both lockfiles exist", func(t *testing.T) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "package-lock.json"), "{}")
		mustWriteFile(t, filepath.Join(dir, "yarn.lock"), "")
		lockfile, manager := detectLockfile(dir)
		if lockfile != "package-lock.json" || manager != "npm" {
			t.Errorf("got (%q, %q), want (package-lock.json, npm)", lockfile, manager)
		}
	})
	t.Run("no lockfile", func(t *testing.T) {
		lockfile, manager := detectLockfile(t.TempDir())
		if lockfile != "" || manager != "" {
			t.Errorf("got (%q, %q), want (\"\", \"\")", lockfile, manager)
		}
	})
}

func TestJSAudit_Run_noLockfile(t *testing.T) {
	findings, err := JSAudit{}.Run(t.TempDir())
	if findings != nil {
		t.Errorf("findings = %+v, want nil", findings)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}
```

- [ ] **Step 3: Run it and watch it fail**

Run: `go test ./internal/adapter/ -run 'TestNpmAuditFindings|TestYarnAuditFindings|TestPnpmAuditFindings|TestJSSeverity|TestDetectLockfile|TestJSAudit_Run_noLockfile' -v`
Expected: FAIL to compile — `undefined: npmAuditFindings` (and `yarnAuditFindings`, `pnpmAuditFindings`, `jsSeverity`, `detectLockfile`, `JSAudit`).

- [ ] **Step 4: Implement `internal/adapter/jsaudit.go`**

```go
package adapter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"codebase-analyser/internal/finding"
)

// JSAudit scans a JS/TS project's dependency tree for known CVEs by
// dispatching to whichever package manager the project's lockfile names:
// npm, yarn or pnpm. It is one adapter rather than three: the package
// manager is a runtime detail of the same job (scan the lockfile for CVEs),
// not a different job. Three adapter types would triple the orchestrator
// wiring and, since only one manager can ever apply to a given lockfile,
// would produce two bogus "skipped tool" notes on every single repo.
//
// JSAudit does not implement adapter.Targeted: a dependency audit has no
// sub-unit to restrict to (one lockfile covers the whole project, not one
// per package or file). cargoaudit.go makes the same choice for the same
// reason.
type JSAudit struct{}

func (JSAudit) Name() string { return "js-audit" }

// CheckInstalled probes for npm specifically. Run dispatches to whichever
// manager the project's lockfile names, but npm is the one Node.js always
// ships, so its presence is what gates whether the orchestrator attempts
// this adapter at all. A yarn-or-pnpm repo on a machine with only npm still
// fails honestly inside Run (exec: "yarn": executable file not found in
// $PATH) rather than silently reporting zero CVEs.
func (JSAudit) CheckInstalled() bool { return commandExists("npm") }

// Install always fails: a package manager cannot bootstrap itself. Auditing
// uses the repo's own package manager, not the analyser's pinned tool cache
// (installJSTools in js.go installs ESLint/TypeScript for linting, which is
// a separate concern), so there is nothing here to install.
func (JSAudit) Install() error {
	return fmt.Errorf("npm not found on PATH; install Node.js to enable JS/TS dependency auditing")
}

// detectLockfile reports which lockfile (if any) is present directly in dir
// and the package manager it implies. Checked in npm/yarn/pnpm order, so
// npm wins if a repo somehow carries both a package-lock.json and a
// yarn.lock. Deliberately not findUp: in a workspace the lockfile lives
// only at the repo root, and Run relies on that to run the audit exactly
// once (see Run's comment).
func detectLockfile(dir string) (lockfile, manager string) {
	candidates := []struct{ file, manager string }{
		{"package-lock.json", "npm"},
		{"npm-shrinkwrap.json", "npm"},
		{"yarn.lock", "yarn"},
		{"pnpm-lock.yaml", "pnpm"},
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(dir, c.file)); err == nil {
			return c.file, c.manager
		}
	}
	return "", ""
}

// jsAuditSeverityTable maps the severity vocabulary shared by npm, yarn and
// pnpm audit output to the analyser's normalized severity.
var jsAuditSeverityTable = map[string]finding.Severity{
	"critical": finding.SeverityCritical,
	"high":     finding.SeverityHigh,
	"moderate": finding.SeverityMedium,
	"low":      finding.SeverityLow,
	"info":     finding.SeverityLow,
}

func jsSeverity(s string) finding.Severity {
	if sev, ok := jsAuditSeverityTable[s]; ok {
		return sev
	}
	return finding.SeverityMedium
}

func (JSAudit) Run(path string) ([]finding.Finding, error) {
	lockfile, manager := detectLockfile(path)
	if manager == "" {
		// No lockfile in this project dir is not an error: in a workspace
		// the lockfile lives only at the repo root, so every member package
		// would otherwise report a bogus "skipped tool" and drag the run's
		// exit code to 2. The root package is detected as its own project
		// and audits the shared lockfile there exactly once.
		return nil, nil
	}

	var findings []finding.Finding
	var err error
	switch manager {
	case "npm":
		var out []byte
		out, err = runCommand(path, "npm", "audit", "--json")
		if err != nil {
			return nil, fmt.Errorf("js-audit: %w", err)
		}
		findings, err = npmAuditFindings(out)
	case "yarn":
		var out []byte
		out, err = runCommand(path, "yarn", "audit", "--json")
		if err != nil {
			return nil, fmt.Errorf("js-audit: %w", err)
		}
		findings, err = yarnAuditFindings(bytes.NewReader(out))
	case "pnpm":
		var out []byte
		out, err = runCommand(path, "pnpm", "audit", "--json")
		if err != nil {
			return nil, fmt.Errorf("js-audit: %w", err)
		}
		findings, err = pnpmAuditFindings(out)
	}
	if err != nil {
		return nil, fmt.Errorf("js-audit: %w", err)
	}
	for i := range findings {
		findings[i].File = lockfile
	}
	return findings, nil
}

// npmVulnerability is the shape of one entry in npm audit --json's top-level
// "vulnerabilities" map, keyed by package name.
type npmVulnerability struct {
	Name     string            `json:"name"`
	Severity string            `json:"severity"`
	Via      []json.RawMessage `json:"via"`
}

type npmAuditOutput struct {
	Vulnerabilities map[string]npmVulnerability `json:"vulnerabilities"`
}

// npmAdvisory is one OBJECT entry of a npmVulnerability's "via" array: a
// real advisory. A bare-string entry in the same array is instead a
// transitive "vulnerable because a dependency is" link with no advisory id
// of its own, and is handled separately in npmAuditFindings.
type npmAdvisory struct {
	Source int    `json:"source"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

// npmAdvisoryRuleID prefers the GHSA id embedded in the advisory URL (e.g.
// "https://github.com/advisories/GHSA-xvch-5gv4-984h") since that's the id
// consumers recognize; falls back to the numeric internal source id.
func npmAdvisoryRuleID(a npmAdvisory) string {
	if idx := bytes.Index([]byte(a.URL), []byte("GHSA-")); idx != -1 {
		return a.URL[idx:]
	}
	return strconv.Itoa(a.Source)
}

// npmAuditFindings parses `npm audit --json` output. via is a heterogeneous
// array: object entries are real advisories, bare-string entries are
// transitive "vulnerable because a dependency is" links. Only object
// entries turn into per-advisory findings — string entries would produce
// duplicate findings with no advisory id. A package whose via is entirely
// strings still gets exactly ONE finding, using the package-level severity
// and a message naming the transitive path, so it isn't silently dropped.
func npmAuditFindings(data []byte) ([]finding.Finding, error) {
	var parsed npmAuditOutput
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parsing npm audit output: %w", err)
	}

	// Sort package names for deterministic output; map iteration order is
	// random.
	names := make([]string, 0, len(parsed.Vulnerabilities))
	for name := range parsed.Vulnerabilities {
		names = append(names, name)
	}
	sort.Strings(names)

	var findings []finding.Finding
	for _, name := range names {
		vuln := parsed.Vulnerabilities[name]
		var transitiveVia []string
		sawAdvisory := false
		for _, raw := range vuln.Via {
			var viaName string
			if err := json.Unmarshal(raw, &viaName); err == nil {
				transitiveVia = append(transitiveVia, viaName)
				continue
			}
			var adv npmAdvisory
			if err := json.Unmarshal(raw, &adv); err == nil {
				sawAdvisory = true
				findings = append(findings, finding.Finding{
					Line:     0,
					Tool:     "js-audit",
					RuleID:   npmAdvisoryRuleID(adv),
					Category: finding.CategorySecurity,
					Severity: jsSeverity(adv.Severity),
					Message:  fmt.Sprintf("%s (%s)", adv.Title, vuln.Name),
				})
			}
		}
		if !sawAdvisory && len(transitiveVia) > 0 {
			via := transitiveVia[0]
			for _, v := range transitiveVia[1:] {
				via += ", " + v
			}
			findings = append(findings, finding.Finding{
				Line:     0,
				Tool:     "js-audit",
				RuleID:   "",
				Category: finding.CategorySecurity,
				Severity: jsSeverity(vuln.Severity),
				Message:  fmt.Sprintf("transitively vulnerable via %s (%s)", via, vuln.Name),
			})
		}
	}
	return findings, nil
}

// yarnAuditLine is the shape of one NDJSON line from `yarn audit --json`.
// Only lines with type "auditAdvisory" carry a finding; every other type
// ("auditSummary", "info", ...) is ignored.
type yarnAuditLine struct {
	Type string `json:"type"`
	Data struct {
		Resolution struct {
			ID int `json:"id"`
		} `json:"resolution"`
		Advisory struct {
			ModuleName       string `json:"module_name"`
			Severity         string `json:"severity"`
			Title            string `json:"title"`
			GithubAdvisoryID string `json:"github_advisory_id"`
		} `json:"advisory"`
	} `json:"data"`
}

// yarnAuditFindings parses `yarn audit --json` NDJSON output, one JSON
// object per line (the same streaming shape clippy.go already decodes for
// cargo's NDJSON, but line-delimited here rather than a concatenated
// stream, so it's read with bufio.Scanner rather than json.Decoder). A line
// that isn't valid JSON, or is JSON of an unrecognized shape, is skipped
// rather than failing the whole parse — a trailing blank line or a stray
// log line from yarn must not drop every real advisory around it.
func yarnAuditFindings(r io.Reader) ([]finding.Finding, error) {
	var findings []finding.Finding
	scanner := bufio.NewScanner(r)
	// yarn audit lines carry full advisory text; grow past bufio.Scanner's
	// 64KiB default token limit.
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var l yarnAuditLine
		if err := json.Unmarshal(line, &l); err != nil {
			continue
		}
		if l.Type != "auditAdvisory" {
			continue
		}
		ruleID := l.Data.Advisory.GithubAdvisoryID
		if ruleID == "" {
			ruleID = strconv.Itoa(l.Data.Resolution.ID)
		}
		findings = append(findings, finding.Finding{
			Line:     0,
			Tool:     "js-audit",
			RuleID:   ruleID,
			Category: finding.CategorySecurity,
			Severity: jsSeverity(l.Data.Advisory.Severity),
			Message:  fmt.Sprintf("%s (%s)", l.Data.Advisory.Title, l.Data.Advisory.ModuleName),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parsing yarn audit output: %w", err)
	}
	return findings, nil
}

// pnpmAdvisory is one entry of pnpm audit --json's "advisories" map (legacy
// npm-v6 shape), keyed by numeric-string advisory id.
type pnpmAdvisory struct {
	ModuleName       string `json:"module_name"`
	Severity         string `json:"severity"`
	Title            string `json:"title"`
	GithubAdvisoryID string `json:"github_advisory_id"`
}

type pnpmAuditOutput struct {
	Advisories map[string]pnpmAdvisory `json:"advisories"`
}

// pnpmAuditFindings parses `pnpm audit --json` output. Advisories is a map
// keyed by numeric-string id — iterated in SORTED key order so output is
// deterministic (map iteration order is random). cargoAuditFindings in
// cargoaudit.go solves the identical problem the identical way for
// cargo-audit's warnings map.
func pnpmAuditFindings(data []byte) ([]finding.Finding, error) {
	var parsed pnpmAuditOutput
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parsing pnpm audit output: %w", err)
	}

	ids := make([]string, 0, len(parsed.Advisories))
	for id := range parsed.Advisories {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	findings := make([]finding.Finding, 0, len(ids))
	for _, id := range ids {
		adv := parsed.Advisories[id]
		ruleID := adv.GithubAdvisoryID
		if ruleID == "" {
			ruleID = id
		}
		findings = append(findings, finding.Finding{
			Line:     0,
			Tool:     "js-audit",
			RuleID:   ruleID,
			Category: finding.CategorySecurity,
			Severity: jsSeverity(adv.Severity),
			Message:  fmt.Sprintf("%s (%s)", adv.Title, adv.ModuleName),
		})
	}
	return findings, nil
}
```

Note on `File`: none of the three parse functions set `Finding.File` — they're tested in isolation against raw tool output and have no notion of which lockfile name Run detected (npm alone has two possible names: `package-lock.json` or `npm-shrinkwrap.json`). `Run` fills in `File: lockfile` on every finding after parsing, once, using the name `detectLockfile` actually found.

- [ ] **Step 5: Run the tests again**

Run: `go test ./internal/adapter/ -run 'TestNpmAuditFindings|TestYarnAuditFindings|TestPnpmAuditFindings|TestJSSeverity|TestDetectLockfile|TestJSAudit_Run_noLockfile' -v`
Expected: PASS, all subtests green.

- [ ] **Final step: Report and stop**

`go build ./... && go test ./...` must be green. Then report what changed, emit ONE single-line commit message, and STOP — the user commits. Never run any mutating git command.
### Task 7: Wire the adapters into the pipeline  *(tier: sonnet)*

**Files:**
- Modify: `internal/orchestrator/orchestrator.go:24-27` (`DefaultAdapters`)
- Modify: `internal/adapter/adapter_test.go:126-139` (`TestOnlyPackageScopedAdaptersAreTargeted`)
- Modify: `internal/cache/packages.go:29-34` (`Units`)
- Modify: `internal/cli/run.go` (lines 48, 49, 146, 148)
- Modify: `internal/cli/run_test.go` (lines 69, 79, 96 — assertion strings)
- Modify: `cmd/analyser/main.go:14`
- Modify: `internal/mcpserver/analyze.go:144` and `mcp_e2e_test.go:66` — **coordinate first, see Step 6**
- Test: `internal/orchestrator/orchestrator_test.go` (append)

**Interfaces:**
- Consumes: `detect.Project.Language` ∈ `{"js","ts"}` (Task 1); `adapter.ESLint` (Task 4), `adapter.Tsc` (Task 5), `adapter.JSAudit` (Task 6).
- Produces: `orchestrator.DefaultAdapters["js"]` and `["ts"]`. Task 8's end-to-end test runs through them.

- [ ] **Step 1: Write the failing wiring test**

Append to `internal/orchestrator/orchestrator_test.go`:

```go
// TestDefaultAdaptersJSTS pins the JS/TS wiring. tsc only appears under
// "ts": running a type-checker against a repo with no tsconfig.json has
// nothing to check, and detect only emits "ts" when one is present.
func TestDefaultAdaptersJSTS(t *testing.T) {
	names := func(lang string) []string {
		var out []string
		for _, a := range orchestrator.DefaultAdapters[lang] {
			out = append(out, a.Name())
		}
		sort.Strings(out)
		return out
	}
	if got, want := names("js"), []string{"eslint", "js-audit"}; !reflect.DeepEqual(got, want) {
		t.Errorf(`DefaultAdapters["js"] = %v, want %v`, got, want)
	}
	if got, want := names("ts"), []string{"eslint", "js-audit", "tsc"}; !reflect.DeepEqual(got, want) {
		t.Errorf(`DefaultAdapters["ts"] = %v, want %v`, got, want)
	}
}
```

Add `"reflect"` and `"sort"` to that file's imports if they are not already there.

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/orchestrator/ -run TestDefaultAdaptersJSTS -v`

Expected: FAIL — `DefaultAdapters["js"] = [], want [eslint js-audit]`.

- [ ] **Step 3: Add the two language keys**

In `internal/orchestrator/orchestrator.go`, replace the `DefaultAdapters` block:

```go
var DefaultAdapters = map[string][]adapter.ToolAdapter{
	"go":   {adapter.GolangciLint{}, adapter.Gosec{}, adapter.Govulncheck{}},
	"rust": {adapter.Clippy{}, adapter.CargoAudit{}},
	// ESLint and the dependency audit apply to any package.json project;
	// tsc is what "ts" buys, and detect only reports "ts" when a
	// tsconfig.json is present for it to read.
	"js": {adapter.ESLint{}, adapter.JSAudit{}},
	"ts": {adapter.ESLint{}, adapter.Tsc{}, adapter.JSAudit{}},
}
```

Run: `go test ./internal/orchestrator/ -run TestDefaultAdaptersJSTS -v` → PASS.

- [ ] **Step 4: Pin the three new adapters as non-Targeted**

`TestOnlyPackageScopedAdaptersAreTargeted` in `internal/adapter/adapter_test.go` records which adapters the incremental cache may restrict. All three new ones must be in the *not*-targeted list. Replace that second loop:

```go
	// Whole-dependency-set scanners, and tools with no sub-unit the cache's
	// Unit model can express: ESLint lints a file tree rather than a package
	// graph, tsc type-checks a whole program, and an audit reads one
	// lockfile. None of them has anything smaller to be restricted to.
	for _, a := range []ToolAdapter{Govulncheck{}, CargoAudit{}, ESLint{}, Tsc{}, JSAudit{}} {
		if _, ok := a.(Targeted); ok {
			t.Errorf("%s implements Targeted but has no restrictable sub-unit", a.Name())
		}
	}
```

- [ ] **Step 5: Make `cache.Units` explicit about languages it does not model**

`cache.Units` currently special-cases `"rust"` and treats everything else as Go — walking for `.go` files. Handed a JS project it would return zero units, and a zero-unit project is indistinguishable from "everything is cached and clean". Nothing reaches that path today (no JS adapter implements `Targeted`, so `RunWithCache` always takes `runFull`), but the trap is one future `RunTargets` method away.

In `internal/cache/packages.go`, replace the opening of `Units`:

```go
func Units(project detect.Project) ([]Unit, error) {
	switch project.Language {
	case "rust":
		return []Unit{{Dir: project.Path, Target: ".", Exts: rustExts}}, nil
	case "go":
		// falls through to the package walk below
	default:
		// JS/TS (and any future language) have no unit model here yet, and
		// none of their adapters implements Targeted, so the orchestrator
		// never asks. Returning nil rather than falling into the Go walk
		// keeps "no units" from ever being read as "nothing changed".
		return nil, nil
	}

	var units []Unit
	// ... rest of the existing function unchanged
```

Append to `internal/cache/packages_test.go`:

```go
func TestUnitsUnknownLanguage(t *testing.T) {
	units, err := cache.Units(detect.Project{Path: t.TempDir(), Language: "js"})
	if err != nil {
		t.Fatal(err)
	}
	if units != nil {
		t.Errorf("Units for a js project = %v, want nil", units)
	}
}
```

Run: `go test ./internal/cache/ ./internal/adapter/ -v`

- [ ] **Step 6: Update the user-facing language strings**

The CLI still says the tool only understands Go and Rust. **Match on the string contents, not on line numbers** — the dashboard session is adding two flags and a `pushRun` call to this same file, so every line number below this point will have moved by the time you get here. Find each old string with `grep -n` and replace it:

In `internal/cli/run.go`:
- `Short: "Analyse a Go/Rust codebase for production-safety issues",` → `Short: "Analyse a Go, Rust, JavaScript or TypeScript codebase for production-safety issues",`
- the `Long` string's first line, `Analyse a Go/Rust codebase for production-safety issues.` → same new wording
- the two `no Go or Rust project found under %s` error returns → `no analysable project found under %s (looked for go.mod, Cargo.toml, package.json)`, keeping each one's existing extra arguments (the first also reports how many directories could not be read)

In `cmd/analyser/main.go`: `Short: "Analyse Go/Rust codebases..."` → `Short: "Analyse Go, Rust and JS/TS codebases for production-safety issues",`

In `internal/cli/run_test.go`: three assertions match on `"no Go or Rust project"` — change each to `"no analysable project"`, including the one inside a comment.

`grep -rn "no Go or Rust project" .` must come back empty except for `internal/mcpserver/` (see below) when you are done.

**Land this wording with the capability, not before it.** The dashboard session declined to make these edits ahead of the feature, and they were right: a CLI that advertises `package.json` support while `detect` still only finds `go.mod` and `Cargo.toml` would tell a Node user "no analysable project found (looked for ... package.json)", which reads as a detection bug rather than an unimplemented feature. Task 1 must be merged before this step.

**Two files here belong to other sessions in this tree — ask before editing:**
- `internal/cli/run.go` is also being modified by the dashboard session (it is adding two flags). Message them before you touch it; the edits are in different parts of the file but must not be made concurrently.
- `internal/mcpserver/analyze.go:144` carries the same `"no Go or Rust project found under %s"` string, and `mcp_e2e_test.go:66` asserts it. That file belongs to the MCP-server session. Message them with the one-line replacement (`no analysable project found under %s (looked for go.mod, Cargo.toml, package.json)`) and let them make it, or get their go-ahead first. Do not edit it unilaterally, and do not leave the two front doors disagreeing about what the tool supports.

If either session is unreachable, make the `internal/cli` changes, leave the MCP string alone, and say so in your report — a stale string in one front door is a smaller problem than two sessions writing the same file.

- [ ] **Step 7: Run the full suite**

Run: `go build ./... && go test ./...`

Expected: PASS. Note for later (no action now): the MCP-server session is adding `adapter.EnvForPath`, a hook `runCommand` consults to set per-repo environment (`GOTOOLCHAIN`, `RUSTUP_TOOLCHAIN`). That is where a Node version pin from `.nvmrc` or `package.json` `engines` belongs when the deferred toolchain work lands — see "Deviation from the spec" at the top of this plan. Do not build it here.

**Never run `go mod tidy` in this tree.** `go.mod` carries several concurrent sessions' dependencies and tidy will silently strip whichever package tree is mid-write. This plan adds no Go dependencies, so `go.mod` should not change at all.

- [ ] **Step 8: Report and stop**

Report what changed, emit ONE single-line commit message, and STOP — the user commits. Never run any mutating git command, and stage explicit paths rather than `git add -A` when they do.

---
### Task 8: Fixture repos and end-to-end coverage  *(tier: sonnet)*

**Files:**
- Create: `testdata/fixtures/js-repo/package.json`
- Create: `testdata/fixtures/js-repo/src/bad.js`
- Create: `testdata/fixtures/ts-repo/package.json`
- Create: `testdata/fixtures/ts-repo/tsconfig.json`
- Create: `testdata/fixtures/ts-repo/src/bad.ts`
- Create: `testdata/fixtures/js-monorepo/package.json`
- Create: `testdata/fixtures/js-monorepo/packages/a/package.json`
- Create: `testdata/fixtures/js-monorepo/packages/a/src/index.js`
- Create: `testdata/fixtures/js-monorepo/packages/b/package.json`
- Create: `testdata/fixtures/js-monorepo/packages/b/src/index.js`
- Create: `testdata/fixtures/js-flat-config/package.json`
- Create: `testdata/fixtures/js-flat-config/eslint.config.mjs`
- Create: `testdata/fixtures/js-legacy-config/package.json`
- Create: `testdata/fixtures/js-legacy-config/.eslintrc.json`
- Modify: `internal/detect/detect_test.go` (append only — do not rewrite the existing tests)
- Create: `internal/adapter/eslintconfig_test.go`
- Create: `jsts_e2e_test.go` (repo root, `package e2e` — matches the existing `e2e_test.go`)

**Interfaces:**
- Consumes: `detect.Detect(root string) ([]detect.Project, []string, error)` and `detect.Project{Path, Language}` from Task 1; `adapter.repoHasESLintConfig(dir string) bool` from Task 4; `orchestrator.DefaultAdapters` (Task 7) indirectly through `cli.Execute`/`cli.RunConfig` exactly as `e2e_test.go` already calls it — no direct import of `internal/adapter`'s ESLint/Tsc/JSAudit types is needed here.
- Produces: fixture repos every later task's tests can point at; no new exported symbols. This is the last task in the plan — nothing downstream consumes it.

**The trap:** `detect.Detect`'s walk skips any directory literally named `testdata` (it is one entry in the `skipDirs` map Task 1 introduces in `internal/detect/detect.go`). All the fixtures created below live under `testdata/fixtures/...`, so every test in this task — detection tests and end-to-end tests alike — MUST call `Detect`/point the pipeline directly AT a fixture directory (e.g. `Detect("../../testdata/fixtures/js-repo")` from `internal/detect`, or `"testdata/fixtures/js-repo"` from the repo root in `jsts_e2e_test.go`). Pointing at the repo root and expecting the walk to find fixtures nested under `testdata/` will silently find nothing — that's an hour of confused debugging, not a bug in `Detect`.

- [ ] **Step 1: Create the JS fixture repo**

`testdata/fixtures/js-repo/package.json`:

```json
{
  "name": "js-repo-fixture",
  "version": "1.0.0",
  "private": true
}
```

`testdata/fixtures/js-repo/src/bad.js`:

```js
function fetchThing() {
  return Promise.resolve({ json: () => ({ ok: true }) });
}

// Floating promise: no return, no .catch() - trips promise/catch-or-return.
fetchThing().then(r => r.json());

// eval on unsanitized input - trips no-eval.
function runUserInput(userInput) {
  eval(userInput);
}

// Reference to a variable that is never declared or imported - trips no-undef.
console.log(undeclaredVariable);

module.exports = { runUserInput };
```

No ESLint config and no lockfile in this fixture — it exercises the
no-config baseline path (Task 4's `repoHasESLintConfig` returns `false`
here) with npm's default install resolution.

- [ ] **Step 2: Create the TS fixture repo**

`testdata/fixtures/ts-repo/package.json`:

```json
{
  "name": "ts-repo-fixture",
  "version": "1.0.0",
  "private": true
}
```

`testdata/fixtures/ts-repo/tsconfig.json`:

```json
{
  "compilerOptions": {
    "strict": true,
    "noEmit": true
  },
  "include": ["src"]
}
```

`testdata/fixtures/ts-repo/src/bad.ts`:

```ts
import { exec } from "child_process";

// Type error: assigning a string to a number - trips tsc's strict type check.
const count: number = "not a number";

function runForUser(userInput: string): void {
  // Command built from a template string, not a string literal - trips
  // security/detect-child-process.
  exec(`ls ${userInput}`, (_err, stdout) => {
    console.log(stdout, count);
  });
}

runForUser(process.argv[2] ?? "");
```

The `tsconfig.json` beside `package.json` is what makes Task 1's detector
classify this fixture as `"ts"` rather than `"js"`.

- [ ] **Step 3: Create the JS monorepo fixture**

`testdata/fixtures/js-monorepo/package.json`:

```json
{
  "name": "js-monorepo-fixture",
  "private": true,
  "workspaces": ["packages/*"]
}
```

`testdata/fixtures/js-monorepo/packages/a/package.json`:

```json
{
  "name": "@fixture/a",
  "version": "1.0.0",
  "private": true
}
```

`testdata/fixtures/js-monorepo/packages/a/src/index.js`:

```js
module.exports = function a() {
  return "a";
};
```

`testdata/fixtures/js-monorepo/packages/b/package.json`:

```json
{
  "name": "@fixture/b",
  "version": "1.0.0",
  "private": true
}
```

`testdata/fixtures/js-monorepo/packages/b/src/index.js`:

```js
module.exports = function b() {
  return "b";
};
```

No lockfile anywhere in this fixture. Three `package.json` files (root,
`packages/a`, `packages/b`) means `Detect` must return three `Project`s —
the walk has no special workspace-awareness, it just finds every
`package.json` under the root.

- [ ] **Step 4: Create the flat-config fixture**

`testdata/fixtures/js-flat-config/package.json`:

```json
{
  "name": "js-flat-config-fixture",
  "version": "1.0.0",
  "private": true
}
```

`testdata/fixtures/js-flat-config/eslint.config.mjs`:

```js
export default [{ rules: { "no-eval": "error" } }];
```

- [ ] **Step 5: Create the legacy-config fixture**

`testdata/fixtures/js-legacy-config/package.json`:

```json
{
  "name": "js-legacy-config-fixture",
  "version": "1.0.0",
  "private": true
}
```

`testdata/fixtures/js-legacy-config/.eslintrc.json`:

```json
{
  "root": true,
  "rules": {
    "no-eval": "error"
  }
}
```

- [ ] **Step 6: Append detection tests to `internal/detect/detect_test.go`**

First, extend the existing import block from:

```go
import (
	"os"
	"path/filepath"
	"testing"
)
```

to:

```go
import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)
```

Then append these three tests at the end of the file (after
`TestDetectUnreadableDirSkippedNotFatal`). Note the paths: this file lives
in `internal/detect/`, so the fixtures created above are reached via
`../../testdata/fixtures/...`, and each test points `Detect` directly at
the fixture directory — never at `../../testdata` or the repo root — per
the trap called out above.

```go
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
```

- [ ] **Step 7: Unit-level ESLint config-format coverage**

This checks Task 4's `repoHasESLintConfig` directly, against the three
fixtures built exactly for this — no ESLint process involved, so it costs
nothing and runs under `-short`.

Create `internal/adapter/eslintconfig_test.go`:

```go
package adapter

import "testing"

// TestRepoHasESLintConfig_detectsAllRecognisedFormats exercises
// repoHasESLintConfig against three fixtures built exactly for this: one
// with a flat config (eslint.config.mjs), one with a legacy config
// (.eslintrc.json), and one with neither. This is deliberately a unit-level
// check on repoHasESLintConfig itself rather than a full ESLint run - a real
// run would exercise the same branch at far higher cost just to observe
// which config file got picked up.
func TestRepoHasESLintConfig_detectsAllRecognisedFormats(t *testing.T) {
	cases := []struct {
		dir  string
		want bool
	}{
		{"../../testdata/fixtures/js-flat-config", true},
		{"../../testdata/fixtures/js-legacy-config", true},
		{"../../testdata/fixtures/js-repo", false},
	}
	for _, c := range cases {
		if got := repoHasESLintConfig(c.dir); got != c.want {
			t.Errorf("repoHasESLintConfig(%q) = %v, want %v", c.dir, got, c.want)
		}
	}
}
```

- [ ] **Step 8: Root-level JS/TS end-to-end test**

These are the only tests in the whole suite that need real Node tooling.
Mirror `e2e_test.go`'s `requireTool` skip-guard exactly (`exec.LookPath`,
`t.Skipf` — never `t.Fatal` when a tool is missing), reuse its
`runE2E`/`runE2EFindings` helpers unmodified, and additionally gate on
`testing.Short()` so `go test -short ./...` stays fast and fully offline —
CI must run the full (non-`-short`) form to actually get this coverage.
The very first invocation of either test triggers Task 4/5/6's
`installJSTools`, which npm-installs the pinned eslint/typescript/plugin
versions into the user cache dir and can take a minute or two; every run
after that reuses the cached install and is fast.

Create `jsts_e2e_test.go`:

```go
package e2e

import (
	"strings"
	"testing"
)

// requireJSTooling gates the JS/TS end-to-end tests. They are the only
// tests in this package that need real Node tooling: npm itself, plus the
// pinned eslint/typescript/eslint-plugin-security install that
// DefaultAdapters shells out to for "js"/"ts" projects (see
// adapter.installJSTools). Also gated behind testing.Short() so
// `go test -short ./...` stays fast and fully offline - run the full
// (non -short) form, e.g. in CI, to exercise this coverage for real.
func requireJSTooling(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("short mode: skipping test that needs real Node tooling")
	}
	requireTool(t, "npm")
}

// TestEndToEnd_JS_findsKnownIssues covers the JS fixture, guarded only by
// the tool DefaultAdapters runs for a "js" project (ESLint, via npm).
func TestEndToEnd_JS_findsKnownIssues(t *testing.T) {
	requireJSTooling(t)

	ruleIDs := runE2E(t, "testdata/fixtures/js-repo")
	joined := strings.Join(ruleIDs, ",")
	if !strings.Contains(joined, "eslint:no-eval") {
		t.Errorf("expected eslint:no-eval (security) in findings, got %s", joined)
	}
}

// TestEndToEnd_TS_findsKnownIssues covers the TS fixture, guarded only by
// the tools DefaultAdapters runs for a "ts" project (ESLint + tsc, via npm).
func TestEndToEnd_TS_findsKnownIssues(t *testing.T) {
	requireJSTooling(t)

	ruleIDs := runE2E(t, "testdata/fixtures/ts-repo")
	joined := strings.Join(ruleIDs, ",")
	var sawTsc bool
	for _, r := range ruleIDs {
		if strings.HasPrefix(r, "tsc:") {
			sawTsc = true
			break
		}
	}
	if !sawTsc {
		t.Errorf("expected a tsc:* (correctness) finding for the string-to-number type error, got %s", joined)
	}
}
```

- [ ] **Final step: Report and stop**

Run: `go build ./... && go test -short ./...`
Expected: PASS. `-short` must not touch npm or the network — only
`TestDetect*` and `TestRepoHasESLintConfig_detectsAllRecognisedFormats` run
for the new fixtures in that mode; `TestEndToEnd_JS_findsKnownIssues` and
`TestEndToEnd_TS_findsKnownIssues` skip themselves. If a Node toolchain is
available and time allows, also run the full form
(`go test ./...`, no `-short`) once to confirm the two new e2e tests
actually pass against real npm/eslint/tsc — the first such run installs the
pinned tooling and can take a minute.

Then report what changed, emit ONE single-line commit message, and STOP —
the user commits. Never run any mutating git command.
### Task 9: Verify every parser against real tool output  *(tier: sonnet)*

**Files:**
- Create: `.superpowers/sdd/2026-08-13-jsts-language-support/real-tool-output/eslint-flat-baseline.json`
- Create: `.superpowers/sdd/2026-08-13-jsts-language-support/real-tool-output/eslint-repo-config.json`
- Create: `.superpowers/sdd/2026-08-13-jsts-language-support/real-tool-output/tsc-noemit.txt`
- Create: `.superpowers/sdd/2026-08-13-jsts-language-support/real-tool-output/npm-audit.json`
- Modify (only if reality disagrees): `internal/adapter/eslint.go`, `tsc.go`, `jsaudit.go` and their tests

**Interfaces:**
- Consumes: everything Tasks 4–8 built, plus the fixture repos under `testdata/fixtures/`.
- Produces: captured real output committed next to the existing `cargo-audit-real-*.json` captures, so the next person changing a parser can diff against reality instead of re-deriving it.

**Why this task exists, and why it is not optional.** This repo has shipped the *same* bug twice. `golangci.go` passed `--out-format json`, a flag removed in golangci-lint v2 — the primary Go linter returned zero findings on every current install. `cargoaudit.go` parsed `advisory.informational`, a field that is always `null` for real vulnerabilities — a whole advisory category silently dropped. **Both had green tests. Both passed code review.** That is the failure mode: a hand-authored fixture and a parser written from the same assumption agree perfectly, so the suite is green and the only party never consulted is the tool. Every parser in Tasks 4, 5 and 6 was written from a fixture in a plan document. None of them has met the real tool.

Requires Node installed. If it is not, this task blocks — say so and stop rather than marking it done.

- [ ] **Step 1: Capture real ESLint output, baseline path**

```bash
mkdir -p .superpowers/sdd/2026-08-13-jsts-language-support/real-tool-output
CACHE=$(mktemp -d)
CODEBASE_ANALYSER_CACHE=$CACHE go run ./cmd/analyser run testdata/fixtures/js-repo --no-llm --format json
"$CACHE/js-tools/node_modules/.bin/eslint" --version
```

Then run the exact command the adapter builds, by hand, and save it:

```bash
"$CACHE/js-tools/node_modules/.bin/eslint" . -f json \
  --no-config-lookup --config "$CACHE/js-tools/eslint.config.mjs" \
  --ignore-pattern 'node_modules/**' --ignore-pattern 'dist/**' \
  --ignore-pattern 'build/**' --ignore-pattern '.next/**' \
  --ignore-pattern 'out/**' --ignore-pattern 'coverage/**' \
  > ../../../.superpowers/sdd/2026-08-13-jsts-language-support/real-tool-output/eslint-flat-baseline.json
```
(run from `testdata/fixtures/js-repo`; adjust the output path if your shell disagrees.)

If that command errors rather than printing JSON, **the adapter is broken and the tests cannot see it** — the flag set is wrong for ESLint 9. Fix `eslint.go`'s argument construction until this exact invocation works, then re-run the analyser end to end.

- [ ] **Step 2: Diff the real ESLint shape against the test fixture**

Compare the captured file against the JSON literal in `eslint_test.go`, field by field:
- Is the top level an array of file results? Does each carry `filePath` and `messages`?
- Does a rule violation carry `ruleId`, `severity` (1 or 2), `message`, `line`?
- Force a parse error into the fixture (add a file containing `function (`) and confirm a fatal message really has `"ruleId": null` and `"fatal": true`, which is what `classifyESLintRule`'s fatal branch depends on.
- Confirm `filePath` is absolute — the orchestrator's `normalizeFilePaths` is what makes it relative, and it only acts on absolute paths.

Any disagreement: fix `eslintFindings` and update the test fixture to the captured bytes. The captured file is the truth.

- [ ] **Step 3: Capture real ESLint output, repo-config path**

```bash
cd testdata/fixtures/js-flat-config
"$CACHE/js-tools/node_modules/.bin/eslint" . -f json > .../real-tool-output/eslint-repo-config.json
```

Confirm that with no `--config` flag ESLint really does discover `eslint.config.mjs` and does not error about a missing config. This is the "a repo's own config always wins" path from the spec, and it is the one that is silently untested at the unit level.

- [ ] **Step 4: Capture real tsc output**

```bash
cd testdata/fixtures/ts-repo
"$CACHE/js-tools/node_modules/.bin/tsc" --noEmit --pretty false \
  > .../real-tool-output/tsc-noemit.txt 2>&1
```

Check against `tsc_test.go`'s fixture:
- Is the shape really `path(line,col): error TSxxxx: message`?
- Does `--pretty false` actually suppress the colour codes and the multi-line source excerpt? If the captured file contains ANSI escapes or indented source lines, the regex will silently match nothing and the adapter will report a clean type-check on a broken repo.
- Does tsc write diagnostics to **stdout** or stderr? `runCommand` returns stdout only. If they land on stderr, the adapter reports zero findings forever — this is exactly the `--out-format json` failure again. Fix by capturing both if needed.

- [ ] **Step 5: Capture real npm audit output**

The `js-repo` fixture has no lockfile, so create a throwaway project with a known-vulnerable dependency:

```bash
D=$(mktemp -d) && cd "$D"
npm init -y >/dev/null
npm install minimist@0.0.8 --package-lock-only --no-audit --no-fund >/dev/null
npm audit --json > .../real-tool-output/npm-audit.json
```

Check against `jsaudit_test.go`'s fixture:
- Is the top-level key `vulnerabilities` (npm 7+) or `advisories` (npm 6)? Print `npm --version` and record it in a comment in the test.
- Is `via` really a heterogeneous array of objects and strings?
- Does an advisory object carry a GHSA id, and under which key (`url`, `source`, or something else)? `RuleID` depends on this.
- Does `npm audit` exit non-zero here? Confirm `runCommand` tolerates it (non-zero exit *with* stdout is success).

If yarn or pnpm is installed, repeat for those; if not, note in the report that those two parsers remain unverified against reality — an honest gap is worth more than an assumed pass.

- [ ] **Step 6: Fix what disagrees, then re-run everything**

For each mismatch: fix the parser first, then replace the test fixture with the real captured bytes (trimmed if huge, never reshaped). A test fixture that has been edited to match the parser is worthless — that is the bug this task exists to prevent.

Run: `go build ./... && go test ./...`

- [ ] **Step 7: Report and stop**

Report: which parsers matched reality unchanged, which needed fixing and how, and which package managers went unverified. Emit ONE single-line commit message, and STOP — the user commits. Never run any mutating git command.

---

## Self-review against the spec

| Spec section | Covered by |
|---|---|
| Detection: `package.json`, `tsconfig.json`, lockfile, workspaces | Tasks 1 (package.json/tsconfig/recursive walk), 4 (workspace globs), 6 (lockfile → manager) |
| Tools wrapped: ESLint / `tsc --noEmit` / npm\|yarn\|pnpm audit | Tasks 4, 5, 6 |
| No dedicated security scanner in v1 | Task 2's baseline enables `eslint-plugin-security`; Task 3 maps its rules into the security category |
| No-config repos get an opinionated baseline, in both config formats | Tasks 2 (both baseline files) and 4 (version-keyed selection, repo config always wins) |
| Severity & category via a curated per-rule lookup | Task 3 |
| Toolchain: pinned, persistent, never global, never the system Node's `node_modules` | Task 2 — **except** the Node *runtime* download, see "Deviation from the spec" above |
| Excluded paths | Task 1 (`skipDirs`) and Task 2 (`jsExcludedDirs`, used by Task 4) |
| Error handling: a failing tool is skipped, never fatal | Existing `orchestrator.runOne`, unchanged; plus the "nothing to do returns `(nil, nil)`" constraint so JS repos do not report false incomplete coverage |
| Testing: per-adapter unit tests on captured output | Tasks 4, 5, 6, and Task 9 which replaces "captured-looking" with actually captured |
| Testing: rule-table unit tests | Task 3 |
| Testing: workspace fixture monorepo | Task 8 (`js-monorepo`) |
| Testing: dual ESLint config format fixtures | Task 8 (`js-flat-config`, `js-legacy-config`, no-config `js-repo`) |
| Testing: end-to-end JS and TS smoke tests | Task 8 (`jsts_e2e_test.go`) |
| Deferred: React/Next.js, bun, Semgrep, Java/Python | Untouched, as specified |
