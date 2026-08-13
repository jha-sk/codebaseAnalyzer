# Codebase Analyser Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A self-hosted Go binary that ingests `analyser run --format json` results from CI and serves a persistent, cross-run web dashboard of code health per repo and branch.

**Architecture:** One new binary (`cmd/dashboard`) in the existing `codebase-analyser` module: `net/http` API over Postgres, with a static single-page frontend embedded via `go:embed`. Storage is dumb (rows in, rows out); every derived number (health score, new/fixed, category counts, top files) is a pure Go function so it is unit-testable without a database. The existing CLI gains a best-effort `--dashboard-url` push.

**Tech Stack:** Go 1.26.5, `net/http` ServeMux method+path routing (stdlib), `github.com/jackc/pgx/v5` stdlib driver over `database/sql`, Postgres 16, React 19 + TypeScript built by Vite into `dist/` and embedded with `go:embed`, Recharts for charts, Vitest + Testing Library for component tests, Docker + docker-compose.

**Spec:** `docs/superpowers/specs/2026-08-13-dashboard-design.md` (read it before Task 1; it is the binding authority where this plan and it disagree)

## Global Constraints

- Module is `codebase-analyser` (Go 1.26.5). The dashboard is a **second binary in the same module**, not a new repo — it reuses `internal/finding` and `internal/report`.
- **NO GIT COMMANDS.** Standing user instruction: never run `git init`/`add`/`commit`/`push`. Each task ends by handing back to the user, who commits.
- Exactly **one new Go dependency** is authorised: `github.com/jackc/pgx/v5`. Everything else server-side is stdlib — no router, no ORM, no migration library.
- The frontend's npm dependencies are exactly: `react`, `react-dom`, `recharts`, and dev-only `vite`, `@vitejs/plugin-react`, `typescript`, `vitest`, `jsdom`, `@testing-library/react`, `@testing-library/user-event`. No CSS framework, no state library, no router, no component kit — the page is one route and its state is four `useState` calls.
- **Chart color is computed, not chosen.** Severity is an *ordered* scale, so charts encode it with the validated ordinal blue ramp `#b7d3f6 → #6da7ec → #2a78d6 → #184f95` (critical→low). The status palette (`good #0ca30c`, `warning #fab219`, `serious #ec835a`, `critical #d03b3b`) is only ever used on a **lone** status mark that also carries a text label — never as a 4-series set (it fails CVD separation at ΔE 4.1 deutan and the normal-vision floor at 13.6; see the ruling below). Single-series bar charts use one color, categorical slot 1 (`#3987e5`). Chart surface is `#1a1a19`.
- Categories are exactly `correctness | concurrency | security | operational`; severities are exactly `critical | high | medium | low` (see `internal/finding/finding.go`).
- The CLI's JSON report shape is fixed and must not change: `{"summary":{"critical":N,"high":N,"medium":N,"low":N},"findings":[{"file","line","tool","ruleID","category","severity","message","explanation"}]}` (see `internal/report/json.go`).
- Push is **best-effort**: an unreachable dashboard prints a warning to stderr and never changes the CLI's exit code.
- Re-pushing the same `(repo, commit_sha)` **overwrites** that run; it never creates a duplicate.
- All branches ingest and are viewable. Retention is unlimited — no pruning job in v1.
- Auth is two tokens, no user accounts: one admin token from env `DASHBOARD_ADMIN_TOKEN`, one ingest token per repo (shown once at registration). Both travel as `Authorization: Bearer <token>`.
- Postgres-backed tests skip cleanly (`t.Skip`) when `DASHBOARD_TEST_DSN` is unset, matching the existing `e2e_test.go` convention.

## Deviations from the spec (deliberate, flagged for the user)

1. **Ingest endpoint is `POST /api/runs`, not `POST /api/repos/:repo/runs`.** The ingest token already identifies exactly one repo, so a repo id in the path is a second source of truth that can only ever disagree with the token. Dropping it also means CI configures one value (`--dashboard-token`), not two. The spec's stated security property ("a leaked ingest token can only push fake data for its own repo") is preserved exactly.
2. **Two read endpoints, not three.** `GET /api/repos/{id}/dashboard?branch=` returns everything the page renders in one response (branches, history, aggregates, current findings) instead of the page stitching together `GET /api/repos/:repo/runs` + `GET /api/runs/:id`. `GET /api/runs/{id}` survives for drill-down into an older run from the activity feed.
3. **React + Vite, not Next.js.** The spec anticipated "an actual component library"; the user asked for React or Next. Next.js only ships here as a static export — there is no SSR behind an admin-token gate and no node runtime in the image — so it would add a framework's weight for none of its features. Vite's `dist/` embeds directly and the single-binary deployment survives intact. Consequence: the Docker build gains a node stage, and `internal/dashboard/web/dist/` is a **committed build artifact** (Go's `//go:embed` fails to compile without it).

4. **Severity is charted with an ordinal ramp, not the status palette.** Running the dataviz validator on the four status hues as a series set fails three checks (lightness band; CVD separation worst pair ΔE 4.1 deutan, target ≥8; normal-vision floor 13.6, hard floor 15) — a four-line severity chart in red/orange/yellow/green is unreadable to a deuteranope and marginal for everyone else. Severity is an ordered scale, so charts use the validated single-hue ordinal blue ramp (all checks pass). Status hues survive where they are correct: one lone status mark carrying a text label — the health tile, the branch health pill, the up/down deltas. **Cost if wrong:** "critical" is no longer red inside the trend chart, which reads as less alarming; the severity cards, health pill and deltas keep status color, so the alarm signal is not lost from the page.
5. **`runs.tools` is a JSONB column, not a table.** Tool run status is display-only data written once and read once; a fourth table buys nothing.
6. **Default branch is picked, not known.** Nothing in the CLI's push tells us the repo's git default branch, so `PickDefaultBranch` prefers `main`, then `master`, then the most recently pushed branch.

## File Structure

**New:**

| File | Responsibility |
|---|---|
| `cmd/dashboard/main.go` | Flag/env parsing, open store, build handler, `http.ListenAndServe` |
| `internal/dashboard/store/schema.sql` | Full DDL, `CREATE ... IF NOT EXISTS`, applied on startup |
| `internal/dashboard/store/store.go` | `Open`, schema apply, repo CRUD, token generation/hashing, URL normalization |
| `internal/dashboard/store/runs.go` | Run upsert + findings replace, branch/run/finding queries |
| `internal/dashboard/api/metrics.go` | Pure derived numbers: health score, new/fixed diff, category counts, top files, default-branch pick |
| `internal/dashboard/api/api.go` | ServeMux wiring, bearer auth, admin + ingest handlers |
| `internal/dashboard/api/dashboard.go` | The one fat read endpoint's payload assembly + run drill-down |
| `internal/dashboard/web/embed.go` | `//go:embed all:dist` of the built SPA |
| `internal/dashboard/web/ui/` | Vite + React + TS source (see below); builds to `../dist` |
| `internal/dashboard/web/dist/` | Committed build output — what the binary serves |

Inside `internal/dashboard/web/ui/`:

| File | Responsibility |
|---|---|
| `package.json`, `vite.config.ts`, `tsconfig.json` | Toolchain; `build.outDir` points at `../dist` |
| `index.html`, `src/main.tsx` | Vite entry, React root |
| `src/types.ts` | TypeScript mirror of the API payload |
| `src/api.ts` | `fetchJSON` with the bearer header, 401 → sign out |
| `src/theme.ts` | The validated color tokens (ordinal severity ramp, status hues, chart chrome) |
| `src/styles.css` | Design tokens, glass panels, the two easing curves, layout |
| `src/App.tsx` | Auth gate, data loading, page composition |
| `src/components/*.tsx` | One file per view (TopBar, BranchTable, SeverityCards, TrendChart, HealthTile, CategoryBars, SinceLastRun, ToolStatus, PipelineDiagram, TopFiles, ActivityFeed, FindingsTable) |
| `src/components/__tests__/*.test.tsx` | Vitest + Testing Library component tests |
| `internal/push/push.go` | Git metadata detection + best-effort POST from the CLI |
| `Dockerfile`, `docker-compose.yml` | Self-hosted deployment |

**Modified:** `internal/cli/run.go` (dashboard flags + push call), `go.mod`/`go.sum` (pgx).

## Running Postgres for tests

Every DB-backed task needs this once; it is not part of any task's diff:

```bash
docker run -d --name analyser-testdb -e POSTGRES_PASSWORD=test -p 5433:5432 postgres:16
export DASHBOARD_TEST_DSN='postgres://postgres:test@localhost:5433/postgres?sslmode=disable'
```

**Always run the DB-backed tests with `-p 1`:**

```bash
go test -p 1 ./internal/dashboard/... -count=1
```

`go test` runs each package's test binary as a **separate process, concurrently**. Both `store` and `api` truncate and repopulate the same tables, so without `-p 1` they interleave and fail — reproducibly, not intermittently, with a different set of tests failing each run (FK violations and deadlocks from concurrent `TRUNCATE ... CASCADE`). `-p 1` serialises packages and the suite passes every time.

> **ponytail: shared-database test isolation by convention, not by construction.** The robust alternative is a schema or database per test process, which means threading a search-path option through `store.Open` — real API surface added purely for tests. `-p 1` costs one flag. Upgrade if the suite ever grows a third DB-backed package, or if anyone actually gets bitten by forgetting the flag.

---

### Task 1: Store — schema, connection, repos and tokens

**Files:**
- Create: `internal/dashboard/store/schema.sql`
- Create: `internal/dashboard/store/store.go`
- Test: `internal/dashboard/store/store_test.go`
- Modify: `go.mod` / `go.sum` (adds `github.com/jackc/pgx/v5`)

**Interfaces:**
- Consumes: nothing (first dashboard task).
- Produces: `store.Open(ctx, dsn) (*Store, error)`, `(*Store).Close() error`, `store.Repo{ID int64; RemoteURL string; RegisteredAt time.Time}`, `(*Store).CreateRepo(ctx, remoteURL string) (Repo, string, error)` (second return is the plaintext token, shown once), `(*Store).ListRepos(ctx) ([]Repo, error)`, `(*Store).RepoByToken(ctx, token string) (Repo, error)`, `(*Store).RegenerateToken(ctx, id int64) (string, error)`, `(*Store).DeleteRepo(ctx, id int64) error`, `store.NormalizeRemoteURL(raw string) string`, `store.ErrNotFound`.

- [ ] **Step 1: Add the pgx dependency**

```bash
go get github.com/jackc/pgx/v5@latest
```

Expected: `go.mod` gains a `require github.com/jackc/pgx/v5 v5.x.x` line.

- [ ] **Step 2: Write `internal/dashboard/store/schema.sql`**

```sql
CREATE TABLE IF NOT EXISTS repos (
    id                BIGSERIAL PRIMARY KEY,
    remote_url        TEXT NOT NULL UNIQUE,
    ingest_token_hash TEXT NOT NULL,
    registered_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS runs (
    id         BIGSERIAL PRIMARY KEY,
    repo_id    BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    branch     TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    pushed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    critical   INT NOT NULL DEFAULT 0,
    high       INT NOT NULL DEFAULT 0,
    medium     INT NOT NULL DEFAULT 0,
    low        INT NOT NULL DEFAULT 0,
    tools      JSONB NOT NULL DEFAULT '[]'::jsonb,
    UNIQUE (repo_id, commit_sha)
);

CREATE TABLE IF NOT EXISTS findings (
    id          BIGSERIAL PRIMARY KEY,
    run_id      BIGINT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    file        TEXT NOT NULL,
    line        INT NOT NULL,
    tool        TEXT NOT NULL,
    rule_id     TEXT NOT NULL,
    category    TEXT NOT NULL,
    severity    TEXT NOT NULL,
    message     TEXT NOT NULL,
    explanation TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS findings_run_idx ON findings (run_id);
CREATE INDEX IF NOT EXISTS runs_branch_idx ON runs (repo_id, branch, pushed_at DESC);
```

- [ ] **Step 3: Write the failing test**

`internal/dashboard/store/store_test.go`:

```go
package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

// testStore opens the shared test database and truncates it, so every test
// starts from an empty schema. Skips when DASHBOARD_TEST_DSN is unset, the
// same convention as the repo's existing e2e_test.go.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DASHBOARD_TEST_DSN")
	if dsn == "" {
		t.Skip("DASHBOARD_TEST_DSN not set, skipping Postgres-backed test")
	}
	s, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.db.Exec(`TRUNCATE repos RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNormalizeRemoteURL(t *testing.T) {
	want := "github.com/acme/widgets"
	for _, in := range []string{
		"git@github.com:acme/widgets.git",
		"https://github.com/acme/widgets.git",
		"https://github.com/acme/widgets",
		"ssh://git@github.com/acme/widgets.git",
		"HTTPS://GitHub.com/Acme/Widgets/",
	} {
		if got := NormalizeRemoteURL(in); got != want {
			t.Errorf("NormalizeRemoteURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCreateRepoIssuesUsableToken(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	repo, token, err := s.CreateRepo(ctx, "git@github.com:acme/widgets.git")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if repo.RemoteURL != "github.com/acme/widgets" {
		t.Errorf("remote_url = %q, want normalized", repo.RemoteURL)
	}
	if len(token) < 32 {
		t.Errorf("token %q is too short to be a secret", token)
	}

	got, err := s.RepoByToken(ctx, token)
	if err != nil {
		t.Fatalf("RepoByToken: %v", err)
	}
	if got.ID != repo.ID {
		t.Errorf("RepoByToken id = %d, want %d", got.ID, repo.ID)
	}
	if _, err := s.RepoByToken(ctx, "not-a-real-token"); err != ErrNotFound {
		t.Errorf("RepoByToken(bogus) err = %v, want ErrNotFound", err)
	}
}

func TestCreateRepoRejectsDuplicate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, _, err := s.CreateRepo(ctx, "github.com/acme/widgets"); err != nil {
		t.Fatalf("first CreateRepo: %v", err)
	}
	_, _, err := s.CreateRepo(ctx, "https://github.com/acme/widgets.git")
	if err == nil {
		t.Fatal("second CreateRepo of the same normalized URL succeeded, want error")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("err = %v, want an 'already registered' message", err)
	}
}

func TestRegenerateTokenInvalidatesOld(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, old, err := s.CreateRepo(ctx, "github.com/acme/widgets")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	fresh, err := s.RegenerateToken(ctx, repo.ID)
	if err != nil {
		t.Fatalf("RegenerateToken: %v", err)
	}
	if fresh == old {
		t.Fatal("RegenerateToken returned the same token")
	}
	if _, err := s.RepoByToken(ctx, old); err != ErrNotFound {
		t.Errorf("old token still works: err = %v, want ErrNotFound", err)
	}
	if _, err := s.RepoByToken(ctx, fresh); err != nil {
		t.Errorf("new token rejected: %v", err)
	}
}

func TestListAndDeleteRepos(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, _, _ := s.CreateRepo(ctx, "github.com/acme/a")
	if _, _, err := s.CreateRepo(ctx, "github.com/acme/b"); err != nil {
		t.Fatalf("CreateRepo b: %v", err)
	}

	repos, err := s.ListRepos(ctx)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("ListRepos returned %d repos, want 2", len(repos))
	}
	if repos[0].RemoteURL != "github.com/acme/a" {
		t.Errorf("ListRepos not ordered by remote_url: got %q first", repos[0].RemoteURL)
	}

	if err := s.DeleteRepo(ctx, a.ID); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	repos, _ = s.ListRepos(ctx)
	if len(repos) != 1 || repos[0].RemoteURL != "github.com/acme/b" {
		t.Errorf("after delete, ListRepos = %+v", repos)
	}
	if err := s.DeleteRepo(ctx, a.ID); err != ErrNotFound {
		t.Errorf("DeleteRepo(missing) err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/dashboard/store/ -v`
Expected: FAIL to build — `undefined: Open`, `undefined: NormalizeRemoteURL`, etc.

- [ ] **Step 5: Write `internal/dashboard/store/store.go`**

```go
// Package store is the dashboard's Postgres layer. It deliberately holds no
// derived logic: every number the UI shows is computed from these rows by
// pure functions in internal/dashboard/api, so they stay testable without a
// database.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schema string

// ErrNotFound is returned for a lookup that matched no row. Callers map it to
// 401 (bad ingest token) or 404 (missing repo/run) as appropriate.
var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

type Repo struct {
	ID           int64     `json:"id"`
	RemoteURL    string    `json:"remote_url"`
	RegisteredAt time.Time `json:"registered_at"`
}

// Open connects and applies the schema. The schema is idempotent DDL rather
// than a migration tool: v1 only ever adds tables, and a self-hosted binary
// that sets itself up on first boot is the whole point of the deployment story.
// ponytail: no migration framework. Add one the first time a column has to
// change shape under live data.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// NormalizeRemoteURL reduces the many spellings of one git remote to a single
// identity: scheme, credentials, .git suffix and trailing slash are stripped
// and the result is lowercased, so git@github.com:acme/w.git and
// https://github.com/acme/w both key the same repo row.
func NormalizeRemoteURL(raw string) string {
	u := strings.TrimSpace(raw)
	u = strings.TrimSuffix(u, "/")
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.Index(u, "@"); i >= 0 {
		u = u[i+1:] // drop user@ (scp-style or ssh:// credentials)
	}
	u = strings.Replace(u, ":", "/", 1) // scp-style host:path -> host/path
	u = strings.TrimSuffix(u, ".git")
	return strings.ToLower(strings.TrimSuffix(u, "/"))
}

// newToken returns a 256-bit URL-safe secret and its storage hash. The token
// is generated here rather than chosen by a human, so it has full entropy and
// a plain SHA-256 is the right store: bcrypt/argon2 defend against brute-force
// on low-entropy human passwords, which this can never be.
func newToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateRepo registers a repo and returns its ingest token in plaintext. That
// return value is the only time the token exists outside a hash - the caller
// must show it to the admin once and then drop it.
func (s *Store) CreateRepo(ctx context.Context, remoteURL string) (Repo, string, error) {
	normalized := NormalizeRemoteURL(remoteURL)
	if normalized == "" {
		return Repo{}, "", errors.New("remote_url is required")
	}
	token, hash, err := newToken()
	if err != nil {
		return Repo{}, "", err
	}
	repo := Repo{RemoteURL: normalized}
	err = s.db.QueryRowContext(ctx,
		`INSERT INTO repos (remote_url, ingest_token_hash) VALUES ($1, $2)
		 ON CONFLICT (remote_url) DO NOTHING
		 RETURNING id, registered_at`,
		normalized, hash).Scan(&repo.ID, &repo.RegisteredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, "", fmt.Errorf("repo %s is already registered", normalized)
	}
	if err != nil {
		return Repo{}, "", fmt.Errorf("insert repo: %w", err)
	}
	return repo, token, nil
}

func (s *Store) ListRepos(ctx context.Context) ([]Repo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, remote_url, registered_at FROM repos ORDER BY remote_url`)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()
	repos := []Repo{}
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.RemoteURL, &r.RegisteredAt); err != nil {
			return nil, fmt.Errorf("scan repo: %w", err)
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

func (s *Store) RepoByID(ctx context.Context, id int64) (Repo, error) {
	var r Repo
	err := s.db.QueryRowContext(ctx,
		`SELECT id, remote_url, registered_at FROM repos WHERE id = $1`, id).
		Scan(&r.ID, &r.RemoteURL, &r.RegisteredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	if err != nil {
		return Repo{}, fmt.Errorf("get repo: %w", err)
	}
	return r, nil
}

// RepoByToken resolves an ingest token to its repo. The lookup is by hash, so
// no comparison against a stored secret happens in Go at all.
func (s *Store) RepoByToken(ctx context.Context, token string) (Repo, error) {
	if token == "" {
		return Repo{}, ErrNotFound
	}
	var r Repo
	err := s.db.QueryRowContext(ctx,
		`SELECT id, remote_url, registered_at FROM repos WHERE ingest_token_hash = $1`,
		hashToken(token)).Scan(&r.ID, &r.RemoteURL, &r.RegisteredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	if err != nil {
		return Repo{}, fmt.Errorf("get repo by token: %w", err)
	}
	return r, nil
}

func (s *Store) RegenerateToken(ctx context.Context, id int64) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE repos SET ingest_token_hash = $1 WHERE id = $2`, hash, id)
	if err != nil {
		return "", fmt.Errorf("regenerate token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return token, nil
}

// DeleteRepo removes the repo and, by ON DELETE CASCADE, all of its runs and
// findings. This is the spec's "revoke a repo's token" in its strongest form.
func (s *Store) DeleteRepo(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM repos WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete repo: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/dashboard/store/ -v -count=1`
Expected: PASS, 5 tests. If they SKIP, the test Postgres is not running — start it (see "Running Postgres for tests") and re-run; a skipped result does not count as passing this task.

- [ ] **Step 7: Verify the module is clean**

Run: `go build ./... && go vet ./... && gofmt -l .`
Expected: no output from any of the three.

- [ ] **Step 8: Hand back to the user**

Do NOT run git commands. Report the files created and stop so the user can commit.

---

### Task 2: Store — run ingest, upsert-by-commit, and queries

**Files:**
- Create: `internal/dashboard/store/runs.go`
- Test: `internal/dashboard/store/runs_test.go`

**Interfaces:**
- Consumes: `store.Store`, `store.Repo`, `store.ErrNotFound` from Task 1.
- Produces: `store.ToolStatus{Name string; Skipped bool; Error string}`, `store.Finding{File string; Line int; Tool, RuleID, Category, Severity, Message, Explanation string}`, `store.Run{ID, RepoID int64; Branch, CommitSHA string; PushedAt time.Time; Counts map[string]int; Tools []ToolStatus}`, `store.Branch{Name string; LastRunAt time.Time; CommitSHA string; RunID int64; Counts map[string]int}`, and the methods `(*Store).SaveRun(ctx, repoID int64, branch, commitSHA string, tools []ToolStatus, findings []Finding) (int64, error)`, `(*Store).Branches(ctx, repoID int64) ([]Branch, error)`, `(*Store).RunsForBranch(ctx, repoID int64, branch string, limit int) ([]Run, error)`, `(*Store).RunByID(ctx, runID int64) (Run, error)`, `(*Store).FindingsForRun(ctx, runID int64) ([]Finding, error)`.

**Ordering contract (later tasks depend on it):** `RunsForBranch` returns **oldest first** — it feeds a left-to-right time axis. `Branches` returns most-recently-active first.

- [ ] **Step 1: Write the failing test**

`internal/dashboard/store/runs_test.go`:

```go
package store

import (
	"context"
	"testing"
)

func fixtureFindings(n int) []Finding {
	out := make([]Finding, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Finding{
			File: "main.go", Line: 10 + i, Tool: "gosec", RuleID: "G101",
			Category: "security", Severity: "high",
			Message: "hardcoded credential", Explanation: "leaks secrets",
		})
	}
	return out
}

func TestSaveRunStoresCountsToolsAndFindings(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	tools := []ToolStatus{
		{Name: "gosec"},
		{Name: "golangci-lint", Skipped: true, Error: "install failed"},
	}
	findings := []Finding{
		{File: "a.go", Line: 1, Tool: "gosec", RuleID: "G101", Category: "security", Severity: "critical", Message: "m1"},
		{File: "a.go", Line: 2, Tool: "gosec", RuleID: "G104", Category: "correctness", Severity: "low", Message: "m2"},
		{File: "b.go", Line: 3, Tool: "gosec", RuleID: "G104", Category: "correctness", Severity: "low", Message: "m3"},
	}
	runID, err := s.SaveRun(ctx, repo.ID, "main", "abc123", tools, findings)
	if err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	run, err := s.RunByID(ctx, runID)
	if err != nil {
		t.Fatalf("RunByID: %v", err)
	}
	if run.Counts["critical"] != 1 || run.Counts["low"] != 2 || run.Counts["high"] != 0 {
		t.Errorf("counts = %v, want critical=1 low=2 high=0", run.Counts)
	}
	if len(run.Tools) != 2 || !run.Tools[1].Skipped || run.Tools[1].Error != "install failed" {
		t.Errorf("tools round-tripped as %+v", run.Tools)
	}

	got, err := s.FindingsForRun(ctx, runID)
	if err != nil {
		t.Fatalf("FindingsForRun: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("FindingsForRun returned %d, want 3", len(got))
	}
	if got[0].File != "a.go" || got[0].Line != 1 || got[0].Severity != "critical" {
		t.Errorf("first finding = %+v", got[0])
	}
}

// The spec's headline ingest requirement: a CI retry pushing the same commit
// overwrites that run rather than creating a second one.
func TestSaveRunUpsertsOnSameCommit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	first, err := s.SaveRun(ctx, repo.ID, "main", "abc123", nil, fixtureFindings(3))
	if err != nil {
		t.Fatalf("first SaveRun: %v", err)
	}
	second, err := s.SaveRun(ctx, repo.ID, "main", "abc123", nil, fixtureFindings(1))
	if err != nil {
		t.Fatalf("second SaveRun: %v", err)
	}
	if first != second {
		t.Errorf("re-push created run %d, want the existing run %d", second, first)
	}

	runs, err := s.RunsForBranch(ctx, repo.ID, "main", 10)
	if err != nil {
		t.Fatalf("RunsForBranch: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("branch has %d runs after a re-push, want 1", len(runs))
	}
	if runs[0].Counts["high"] != 1 {
		t.Errorf("counts = %v, want the re-pushed high=1, not the original 3", runs[0].Counts)
	}
	got, _ := s.FindingsForRun(ctx, first)
	if len(got) != 1 {
		t.Errorf("run has %d findings after a re-push, want the 1 that replaced them", len(got))
	}
}

func TestRunsForBranchIsOldestFirstAndScoped(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	for _, c := range []string{"c1", "c2", "c3"} {
		if _, err := s.SaveRun(ctx, repo.ID, "main", c, nil, fixtureFindings(1)); err != nil {
			t.Fatalf("SaveRun %s: %v", c, err)
		}
	}
	if _, err := s.SaveRun(ctx, repo.ID, "feature", "d1", nil, fixtureFindings(1)); err != nil {
		t.Fatalf("SaveRun feature: %v", err)
	}

	runs, err := s.RunsForBranch(ctx, repo.ID, "main", 10)
	if err != nil {
		t.Fatalf("RunsForBranch: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs on main, want 3 (feature must not leak in)", len(runs))
	}
	if runs[0].CommitSHA != "c1" || runs[2].CommitSHA != "c3" {
		t.Errorf("order = %s,%s,%s, want oldest-first c1,c2,c3", runs[0].CommitSHA, runs[1].CommitSHA, runs[2].CommitSHA)
	}

	// limit keeps the NEWEST n, still returned oldest-first.
	runs, _ = s.RunsForBranch(ctx, repo.ID, "main", 2)
	if len(runs) != 2 || runs[0].CommitSHA != "c2" || runs[1].CommitSHA != "c3" {
		t.Errorf("limited runs = %+v, want the newest two c2,c3 in that order", runs)
	}
}

func TestBranchesSummarisesEachBranchOnce(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	if _, err := s.SaveRun(ctx, repo.ID, "main", "c1", nil, fixtureFindings(1)); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	if _, err := s.SaveRun(ctx, repo.ID, "main", "c2", nil, fixtureFindings(2)); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	last, err := s.SaveRun(ctx, repo.ID, "feature", "d1", nil, fixtureFindings(5))
	if err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	branches, err := s.Branches(ctx, repo.ID)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if len(branches) != 2 {
		t.Fatalf("got %d branches, want 2", len(branches))
	}
	if branches[0].Name != "feature" {
		t.Errorf("branches[0] = %q, want the most recently active branch 'feature'", branches[0].Name)
	}
	if branches[0].RunID != last || branches[0].Counts["high"] != 5 {
		t.Errorf("feature summary = %+v, want its latest run's counts", branches[0])
	}
	if branches[1].Name != "main" || branches[1].CommitSHA != "c2" {
		t.Errorf("main summary = %+v, want the latest commit c2", branches[1])
	}
}

func TestRunByIDMissing(t *testing.T) {
	s := testStore(t)
	if _, err := s.RunByID(context.Background(), 99999); err != ErrNotFound {
		t.Errorf("RunByID(missing) = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/dashboard/store/ -run 'TestSaveRun|TestRuns|TestBranches|TestRunByID' -v`
Expected: FAIL to build — `undefined: ToolStatus`, `undefined: Finding`, etc.

- [ ] **Step 3: Write `internal/dashboard/store/runs.go`**

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ToolStatus mirrors one orchestrator.ToolResult as the CLI pushes it: which
// tool ran, and if it did not, why. Stored as JSONB on the run rather than in
// its own table - it is written once and read once, purely for display.
type ToolStatus struct {
	Name    string `json:"name"`
	Skipped bool   `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

// Finding is one row of the CLI's JSON report. Field names match that
// report's JSON keys so the ingest handler can decode straight into it.
type Finding struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Tool        string `json:"tool"`
	RuleID      string `json:"ruleID"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
	Explanation string `json:"explanation"`
}

type Run struct {
	ID        int64          `json:"id"`
	RepoID    int64          `json:"repo_id"`
	Branch    string         `json:"branch"`
	CommitSHA string         `json:"commit_sha"`
	PushedAt  time.Time      `json:"pushed_at"`
	Counts    map[string]int `json:"counts"`
	Tools     []ToolStatus   `json:"tools"`
}

type Branch struct {
	Name      string         `json:"name"`
	LastRunAt time.Time      `json:"last_run_at"`
	CommitSHA string         `json:"commit_sha"`
	RunID     int64          `json:"run_id"`
	Counts    map[string]int `json:"counts"`
}

// severities is the fixed key set for every Counts map, so the UI can index
// all four without nil checks even when a severity has no findings.
var severities = [4]string{"critical", "high", "medium", "low"}

func countBySeverity(findings []Finding) map[string]int {
	counts := map[string]int{}
	for _, s := range severities {
		counts[s] = 0
	}
	for _, f := range findings {
		if _, known := counts[f.Severity]; known {
			counts[f.Severity]++
		}
	}
	return counts
}

// SaveRun writes one CI push. It is an upsert on (repo_id, commit_sha): a
// retry of the same commit replaces that run's counts, tool statuses and
// findings wholesale rather than adding a second row. The whole thing runs in
// one transaction, so a failure mid-write can never leave a run whose stored
// counts disagree with its stored findings.
func (s *Store) SaveRun(ctx context.Context, repoID int64, branch, commitSHA string, tools []ToolStatus, findings []Finding) (int64, error) {
	if branch == "" || commitSHA == "" {
		return 0, errors.New("branch and commit are required")
	}
	if tools == nil {
		tools = []ToolStatus{}
	}
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return 0, fmt.Errorf("encode tool statuses: %w", err)
	}
	counts := countBySeverity(findings)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op once Commit has succeeded

	var runID int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO runs (repo_id, branch, commit_sha, critical, high, medium, low, tools)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (repo_id, commit_sha) DO UPDATE SET
		   branch = EXCLUDED.branch, pushed_at = now(),
		   critical = EXCLUDED.critical, high = EXCLUDED.high,
		   medium = EXCLUDED.medium, low = EXCLUDED.low, tools = EXCLUDED.tools
		 RETURNING id`,
		repoID, branch, commitSHA,
		counts["critical"], counts["high"], counts["medium"], counts["low"],
		toolsJSON).Scan(&runID)
	if err != nil {
		return 0, fmt.Errorf("upsert run: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM findings WHERE run_id = $1`, runID); err != nil {
		return 0, fmt.Errorf("clear previous findings: %w", err)
	}
	// ponytail: one INSERT per finding inside the transaction. A run carries
	// tens to low hundreds of findings, so this is milliseconds; switch to
	// pgx.CopyFrom if a run ever pushes tens of thousands.
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO findings (run_id, file, line, tool, rule_id, category, severity, message, explanation)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`)
	if err != nil {
		return 0, fmt.Errorf("prepare finding insert: %w", err)
	}
	defer stmt.Close()
	for _, f := range findings {
		if _, err := stmt.ExecContext(ctx, runID, f.File, f.Line, f.Tool, f.RuleID,
			f.Category, f.Severity, f.Message, f.Explanation); err != nil {
			return 0, fmt.Errorf("insert finding %s:%d: %w", f.File, f.Line, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit run: %w", err)
	}
	return runID, nil
}

func scanRun(rows interface{ Scan(...any) error }) (Run, error) {
	var (
		r         Run
		toolsJSON []byte
		c, h, m, l int
	)
	if err := rows.Scan(&r.ID, &r.RepoID, &r.Branch, &r.CommitSHA, &r.PushedAt, &c, &h, &m, &l, &toolsJSON); err != nil {
		return Run{}, err
	}
	r.Counts = map[string]int{"critical": c, "high": h, "medium": m, "low": l}
	r.Tools = []ToolStatus{}
	if err := json.Unmarshal(toolsJSON, &r.Tools); err != nil {
		return Run{}, fmt.Errorf("decode tool statuses: %w", err)
	}
	return r, nil
}

const runColumns = `id, repo_id, branch, commit_sha, pushed_at, critical, high, medium, low, tools`

func (s *Store) RunByID(ctx context.Context, runID int64) (Run, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, runID)
	r, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("get run: %w", err)
	}
	return r, nil
}

// RunsForBranch returns up to limit runs, keeping the NEWEST ones but
// returning them oldest-first so the trend chart can render them left to
// right without reversing.
func (s *Store) RunsForBranch(ctx context.Context, repoID int64, branch string, limit int) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT * FROM (
		   SELECT `+runColumns+` FROM runs
		   WHERE repo_id = $1 AND branch = $2
		   ORDER BY pushed_at DESC, id DESC LIMIT $3
		 ) newest ORDER BY pushed_at ASC, id ASC`,
		repoID, branch, limit)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// Branches summarises every branch that has ever pushed, by its latest run,
// most recently active first. DISTINCT ON is Postgres's direct way to say
// "one row per branch, the newest" without a self-join.
func (s *Store) Branches(ctx context.Context, repoID int64) ([]Branch, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT branch, pushed_at, commit_sha, id, critical, high, medium, low FROM (
		   SELECT DISTINCT ON (branch) branch, pushed_at, commit_sha, id, critical, high, medium, low
		   FROM runs WHERE repo_id = $1
		   ORDER BY branch, pushed_at DESC, id DESC
		 ) latest ORDER BY pushed_at DESC`, repoID)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	defer rows.Close()
	branches := []Branch{}
	for rows.Next() {
		var (
			b          Branch
			c, h, m, l int
		)
		if err := rows.Scan(&b.Name, &b.LastRunAt, &b.CommitSHA, &b.RunID, &c, &h, &m, &l); err != nil {
			return nil, fmt.Errorf("scan branch: %w", err)
		}
		b.Counts = map[string]int{"critical": c, "high": h, "medium": m, "low": l}
		branches = append(branches, b)
	}
	return branches, rows.Err()
}

// FindingsForRun returns a run's findings in a stable order (file, then line),
// so two renders of the same run never disagree.
func (s *Store) FindingsForRun(ctx context.Context, runID int64) ([]Finding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT file, line, tool, rule_id, category, severity, message, explanation
		 FROM findings WHERE run_id = $1 ORDER BY file, line, rule_id, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list findings: %w", err)
	}
	defer rows.Close()
	findings := []Finding{}
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.File, &f.Line, &f.Tool, &f.RuleID, &f.Category,
			&f.Severity, &f.Message, &f.Explanation); err != nil {
			return nil, fmt.Errorf("scan finding: %w", err)
		}
		findings = append(findings, f)
	}
	return findings, rows.Err()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/dashboard/store/ -v -count=1`
Expected: PASS, 10 tests (Task 1's 5 plus these 5). A SKIP is not a pass — start the test Postgres first.

- [ ] **Step 5: Prove the upsert test can fail**

Temporarily change the `ON CONFLICT (repo_id, commit_sha) DO UPDATE SET ...` clause to `ON CONFLICT DO NOTHING`, re-run `go test ./internal/dashboard/store/ -run TestSaveRunUpserts -count=1`, and confirm it FAILS. Restore the clause and confirm it passes again. A determinism/upsert guard that cannot fail is worse than none.

- [ ] **Step 6: Verify the module is clean**

Run: `go build ./... && go vet ./... && gofmt -l .`
Expected: no output.

- [ ] **Step 7: Hand back to the user**

Do NOT run git commands. Report the files created and stop so the user can commit.

---

### Task 3: Derived metrics (pure functions, no database)

Every number the dashboard shows that is not a raw stored count lives here. Keeping it out of SQL is what makes it testable in milliseconds with no Postgres.

**Files:**
- Create: `internal/dashboard/api/metrics.go`
- Test: `internal/dashboard/api/metrics_test.go`

**Interfaces:**
- Consumes: `store.Finding`, `store.Branch` from Task 2.
- Produces: `api.HealthScore(cur, prev map[string]int) int`, `api.Fingerprint(f store.Finding) string`, `api.Diff(prev, cur []store.Finding) (added, fixed int)`, `api.CategoryCounts(fs []store.Finding) map[string]int`, `api.FileCount{File string; Count int}`, `api.TopFiles(fs []store.Finding, n int) []FileCount`, `api.PickDefaultBranch(branches []store.Branch) string`.

- [ ] **Step 1: Write the failing test**

`internal/dashboard/api/metrics_test.go`:

```go
package api

import (
	"testing"
	"time"

	"codebase-analyser/internal/dashboard/store"
)

func counts(critical, high, medium, low int) map[string]int {
	return map[string]int{"critical": critical, "high": high, "medium": medium, "low": low}
}

func TestHealthScoreWeightsBySeverity(t *testing.T) {
	if got := HealthScore(counts(0, 0, 0, 0), nil); got != 100 {
		t.Errorf("clean run scored %d, want 100", got)
	}
	// One critical must cost more than one low.
	crit := HealthScore(counts(1, 0, 0, 0), nil)
	low := HealthScore(counts(0, 0, 0, 1), nil)
	if crit >= low {
		t.Errorf("critical scored %d and low scored %d; critical must cost more", crit, low)
	}
	if got := HealthScore(counts(50, 50, 50, 50), nil); got != 0 {
		t.Errorf("catastrophic run scored %d, want the floor 0", got)
	}
}

func TestHealthScoreIsTrendAdjusted(t *testing.T) {
	cur := counts(0, 2, 0, 0)
	improving := HealthScore(cur, counts(0, 5, 0, 0))
	flat := HealthScore(cur, counts(0, 2, 0, 0))
	worsening := HealthScore(cur, counts(0, 1, 0, 0))
	if !(improving > flat && flat > worsening) {
		t.Errorf("improving=%d flat=%d worsening=%d; want improving > flat > worsening", improving, flat, worsening)
	}
	if HealthScore(cur, nil) != flat {
		t.Error("a first-ever run must score the same as a flat trend, not be penalised")
	}
	// The adjustment must never push the score outside 0..100.
	if got := HealthScore(counts(0, 0, 0, 0), counts(0, 9, 0, 0)); got != 100 {
		t.Errorf("perfect run after a bad one scored %d, want the ceiling 100", got)
	}
}

func TestDiffCountsNewAndFixed(t *testing.T) {
	prev := []store.Finding{
		{File: "a.go", Line: 10, Tool: "gosec", RuleID: "G101", Message: "m1"},
		{File: "b.go", Line: 20, Tool: "gosec", RuleID: "G104", Message: "m2"},
	}
	cur := []store.Finding{
		// same finding, line drifted by an unrelated edit above it - not new.
		{File: "a.go", Line: 14, Tool: "gosec", RuleID: "G101", Message: "m1"},
		{File: "c.go", Line: 1, Tool: "clippy", RuleID: "unwrap_used", Message: "m3"},
	}
	added, fixed := Diff(prev, cur)
	if added != 1 {
		t.Errorf("added = %d, want 1 (only c.go is new)", added)
	}
	if fixed != 1 {
		t.Errorf("fixed = %d, want 1 (only b.go went away)", fixed)
	}

	if a, f := Diff(nil, cur); a != 2 || f != 0 {
		t.Errorf("first-ever run: added=%d fixed=%d, want 2 and 0", a, f)
	}
	if a, f := Diff(prev, nil); a != 0 || f != 2 {
		t.Errorf("everything fixed: added=%d fixed=%d, want 0 and 2", a, f)
	}
}

func TestCategoryCountsAlwaysHasFourKeys(t *testing.T) {
	got := CategoryCounts([]store.Finding{
		{Category: "security"}, {Category: "security"}, {Category: "concurrency"},
		{Category: "nonsense"}, // unknown categories are ignored, not counted
	})
	if len(got) != 4 {
		t.Fatalf("got %d keys (%v), want exactly the four spec categories", len(got), got)
	}
	if got["security"] != 2 || got["concurrency"] != 1 || got["correctness"] != 0 || got["operational"] != 0 {
		t.Errorf("counts = %v", got)
	}
}

func TestTopFilesRanksAndTruncates(t *testing.T) {
	fs := []store.Finding{
		{File: "b.go"}, {File: "b.go"}, {File: "b.go"},
		{File: "a.go"}, {File: "a.go"},
		{File: "c.go"},
		{File: "d.go"},
	}
	got := TopFiles(fs, 3)
	if len(got) != 3 {
		t.Fatalf("got %d files, want 3", len(got))
	}
	if got[0].File != "b.go" || got[0].Count != 3 || got[1].File != "a.go" {
		t.Errorf("ranking = %+v, want b.go(3) then a.go(2)", got)
	}
	// c.go and d.go tie at 1; the tie must break alphabetically so the view is
	// stable across renders rather than shuffling with map iteration.
	if got[2].File != "c.go" {
		t.Errorf("tie broke to %q, want the alphabetically first 'c.go'", got[2].File)
	}
	if len(TopFiles(nil, 3)) != 0 {
		t.Error("TopFiles(nil) must be empty, not nil-panicking")
	}
}

func TestPickDefaultBranch(t *testing.T) {
	now := time.Now()
	branches := []store.Branch{
		{Name: "feature-x", LastRunAt: now},
		{Name: "master", LastRunAt: now.Add(-time.Hour)},
		{Name: "main", LastRunAt: now.Add(-2 * time.Hour)},
	}
	if got := PickDefaultBranch(branches); got != "main" {
		t.Errorf("got %q, want main even though it is not the most recent", got)
	}
	if got := PickDefaultBranch(branches[:2]); got != "master" {
		t.Errorf("got %q, want master when there is no main", got)
	}
	if got := PickDefaultBranch(branches[:1]); got != "feature-x" {
		t.Errorf("got %q, want the most recent branch when neither main nor master exists", got)
	}
	if got := PickDefaultBranch(nil); got != "" {
		t.Errorf("got %q, want empty for a repo that has never pushed", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/dashboard/api/ -v`
Expected: FAIL to build — `undefined: HealthScore` and friends.

- [ ] **Step 3: Write `internal/dashboard/api/metrics.go`**

```go
// Package api serves the dashboard's HTTP surface. This file holds every
// derived number the UI shows - deliberately as pure functions over store
// rows, so they are unit-testable without a database and the SQL stays dumb.
package api

import (
	"sort"

	"codebase-analyser/internal/dashboard/store"
)

// severityWeight prices one finding of each severity against the 100-point
// health budget: a critical costs twelve times a low.
var severityWeight = map[string]int{"critical": 12, "high": 6, "medium": 2, "low": 1}

// categories is the spec's fixed set; CategoryCounts always returns all four
// so the UI can render a breakdown without nil checks.
var categories = [4]string{"correctness", "concurrency", "security", "operational"}

// trendAdjustment is what a run gains for improving on the previous one, or
// loses for regressing. Small on purpose: the score is dominated by what is
// wrong now, nudged by which way it is heading.
const trendAdjustment = 5

// HealthScore reduces a run to a single 0-100 signal: a severity-weighted
// penalty against a perfect 100, nudged by the direction of travel since the
// previous run. prev may be nil (first run on a branch), which scores as a
// flat trend rather than a penalty.
func HealthScore(cur, prev map[string]int) int {
	penalty := 0
	for sev, weight := range severityWeight {
		penalty += cur[sev] * weight
	}
	score := 100 - penalty
	if prev != nil {
		switch curTotal, prevTotal := total(cur), total(prev); {
		case curTotal < prevTotal:
			score += trendAdjustment
		case curTotal > prevTotal:
			score -= trendAdjustment
		}
	}
	return clamp(score, 0, 100)
}

func total(counts map[string]int) int {
	n := 0
	for _, c := range counts {
		n += c
	}
	return n
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Fingerprint identifies "the same problem" across runs. Line number is
// deliberately excluded: an unrelated edit higher up the file shifts every
// line below it, and reporting a whole file as newly broken because someone
// added an import would make the new/fixed counters useless.
// ponytail: two identical findings in one file collapse to one fingerprint,
// so a run that goes from three to one instance of the same rule reports no
// change. Add an occurrence index here if that granularity is ever wanted.
func Fingerprint(f store.Finding) string {
	return f.File + "\x00" + f.Tool + "\x00" + f.RuleID + "\x00" + f.Message
}

// Diff reports how many findings are new in cur and how many present in prev
// are gone. Either side may be nil.
func Diff(prev, cur []store.Finding) (added, fixed int) {
	prevSet := fingerprints(prev)
	curSet := fingerprints(cur)
	for fp := range curSet {
		if !prevSet[fp] {
			added++
		}
	}
	for fp := range prevSet {
		if !curSet[fp] {
			fixed++
		}
	}
	return added, fixed
}

func fingerprints(fs []store.Finding) map[string]bool {
	set := make(map[string]bool, len(fs))
	for _, f := range fs {
		set[Fingerprint(f)] = true
	}
	return set
}

// CategoryCounts answers "where does the risk concentrate", which the
// severity cards cannot show. Unknown categories are ignored rather than
// added as a fifth key, so the UI's four columns are guaranteed.
func CategoryCounts(fs []store.Finding) map[string]int {
	out := map[string]int{}
	for _, c := range categories {
		out[c] = 0
	}
	for _, f := range fs {
		if _, known := out[f.Category]; known {
			out[f.Category]++
		}
	}
	return out
}

type FileCount struct {
	File  string `json:"file"`
	Count int    `json:"count"`
}

// TopFiles ranks files by finding count, most first, ties broken
// alphabetically so the view does not reshuffle between identical renders.
func TopFiles(fs []store.Finding, n int) []FileCount {
	byFile := map[string]int{}
	for _, f := range fs {
		byFile[f.File]++
	}
	out := make([]FileCount, 0, len(byFile))
	for file, count := range byFile {
		out = append(out, FileCount{File: file, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].File < out[j].File
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// PickDefaultBranch chooses which branch the page opens on. Nothing the CLI
// pushes tells us the repo's real git default branch, so this prefers the two
// names that almost always are one, and otherwise falls back to whichever
// branch pushed most recently (Branches is already ordered that way).
func PickDefaultBranch(branches []store.Branch) string {
	for _, preferred := range []string{"main", "master"} {
		for _, b := range branches {
			if b.Name == preferred {
				return preferred
			}
		}
	}
	if len(branches) > 0 {
		return branches[0].Name
	}
	return ""
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/dashboard/api/ -v -count=1`
Expected: PASS, 6 tests. These need no database and must never skip.

- [ ] **Step 5: Verify the module is clean**

Run: `go build ./... && go vet ./... && gofmt -l .`
Expected: no output.

- [ ] **Step 6: Hand back to the user**

Do NOT run git commands. Report the files created and stop so the user can commit.

---

### Task 4: API — auth, admin repo management, and ingest

**Files:**
- Create: `internal/dashboard/api/api.go`
- Test: `internal/dashboard/api/api_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1-3.
- Produces: `api.New(st *store.Store, adminToken string, assets fs.FS) http.Handler`, and the wire format `api.ingestRequest` documented below. Task 5 adds handlers to the same mux inside `New`; Task 6's CLI pushes exactly this JSON.

**Ingest wire format** (Task 6 must send precisely this):

```json
{
  "branch": "main",
  "commit": "abc123",
  "tools": [{"name": "gosec", "skipped": false}, {"name": "clippy", "skipped": true, "error": "install failed"}],
  "report": {"summary": {"critical": 0, "high": 1, "medium": 0, "low": 0}, "findings": [ ... ]}
}
```

`report` is the CLI's existing JSON report verbatim. `summary` is accepted but ignored — the server recounts from `findings`, so a malformed or stale summary can never make the stored counts disagree with the stored findings.

> **Carried forward from Task 2:** `store.SaveRun` now validates every finding's severity and category before it opens its transaction, returning an error wrapping the sentinel `store.ErrInvalidFinding`. The ingest handler MUST check `errors.Is(err, store.ErrInvalidFinding)` and reply **400** with the error's message, falling through to `serverError`'s 500 only for anything else. A report carrying an unrecognised severity is a malformed client request, not a server fault, and a 500 would tell CI to retry something that can never succeed. Cover it with a test that pushes a finding with severity `"blocker"` and asserts 400.

- [ ] **Step 1: Write the failing test**

`internal/dashboard/api/api_test.go`:

```go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"testing/fstest"

	"codebase-analyser/internal/dashboard/store"
)

const testAdminToken = "admin-secret"

// newTestServer boots the real handler over the real test database.
func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	dsn := os.Getenv("DASHBOARD_TEST_DSN")
	if dsn == "" {
		t.Skip("DASHBOARD_TEST_DSN not set, skipping Postgres-backed test")
	}
	st, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.Reset(context.Background()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	srv := httptest.NewServer(New(st, testAdminToken, fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>dash</title>")},
	}))
	t.Cleanup(func() { srv.Close(); st.Close() })
	return srv, st
}

func do(t *testing.T, srv *httptest.Server, method, path, token string, body any) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req, err := http.NewRequest(method, srv.URL+path, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out := new(bytes.Buffer)
	out.ReadFrom(resp.Body)
	return resp, out.Bytes()
}

func registerRepo(t *testing.T, srv *httptest.Server, remote string) (int64, string) {
	t.Helper()
	resp, body := do(t, srv, "POST", "/api/admin/repos", testAdminToken,
		map[string]string{"remote_url": remote})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: status %d, body %s", resp.StatusCode, body)
	}
	var out struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return out.ID, out.Token
}

func TestAdminEndpointsRequireAdminToken(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, tc := range []struct{ method, path, token string }{
		{"POST", "/api/admin/repos", ""},
		{"POST", "/api/admin/repos", "wrong"},
		{"GET", "/api/admin/repos", "wrong"},
	} {
		resp, _ := do(t, srv, tc.method, tc.path, tc.token, map[string]string{"remote_url": "github.com/a/b"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s with token %q: status %d, want 401", tc.method, tc.path, tc.token, resp.StatusCode)
		}
	}
}

func TestRegisterRepoReturnsTokenOnce(t *testing.T) {
	srv, _ := newTestServer(t)
	id, token := registerRepo(t, srv, "git@github.com:acme/widgets.git")
	if id == 0 || len(token) < 32 {
		t.Fatalf("register returned id=%d token=%q", id, token)
	}

	resp, body := do(t, srv, "GET", "/api/admin/repos", testAdminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status %d", resp.StatusCode)
	}
	if bytes.Contains(body, []byte(token)) {
		t.Error("the repo list leaked the ingest token; it must be shown only at registration")
	}
	if !bytes.Contains(body, []byte("github.com/acme/widgets")) {
		t.Errorf("list body = %s, want the normalized remote URL", body)
	}

	// Duplicate registration is a client error, not a 500.
	resp, _ = do(t, srv, "POST", "/api/admin/repos", testAdminToken,
		map[string]string{"remote_url": "https://github.com/acme/widgets"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate register: status %d, want 409", resp.StatusCode)
	}

	// Missing remote_url is a 400.
	resp, _ = do(t, srv, "POST", "/api/admin/repos", testAdminToken, map[string]string{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty remote_url: status %d, want 400", resp.StatusCode)
	}
}

func TestIngestStoresTheRun(t *testing.T) {
	srv, st := newTestServer(t)
	repoID, token := registerRepo(t, srv, "github.com/acme/widgets")

	payload := map[string]any{
		"branch": "main",
		"commit": "abc123",
		"tools":  []map[string]any{{"name": "gosec"}, {"name": "clippy", "skipped": true, "error": "install failed"}},
		"report": map[string]any{
			"summary": map[string]int{"critical": 99, "high": 99, "medium": 99, "low": 99}, // ignored on purpose
			"findings": []map[string]any{
				{"file": "a.go", "line": 4, "tool": "gosec", "ruleID": "G101",
					"category": "security", "severity": "critical", "message": "m", "explanation": "e"},
			},
		},
	}
	resp, body := do(t, srv, "POST", "/api/runs", token, payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ingest: status %d, body %s", resp.StatusCode, body)
	}

	runs, err := st.RunsForBranch(context.Background(), repoID, "main", 10)
	if err != nil {
		t.Fatalf("RunsForBranch: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("stored %d runs, want 1", len(runs))
	}
	if runs[0].Counts["critical"] != 1 || runs[0].Counts["high"] != 0 {
		t.Errorf("counts = %v; the server must recount from findings, not trust the pushed summary", runs[0].Counts)
	}
	if len(runs[0].Tools) != 2 || !runs[0].Tools[1].Skipped {
		t.Errorf("tool statuses = %+v", runs[0].Tools)
	}
}

func TestIngestRejectsBadTokensAndInput(t *testing.T) {
	srv, _ := newTestServer(t)
	_, token := registerRepo(t, srv, "github.com/acme/widgets")
	good := map[string]any{"branch": "main", "commit": "abc123",
		"report": map[string]any{"findings": []any{}}}

	resp, _ := do(t, srv, "POST", "/api/runs", "not-a-token", good)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bogus ingest token: status %d, want 401", resp.StatusCode)
	}
	resp, _ = do(t, srv, "POST", "/api/runs", testAdminToken, good)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("admin token used for ingest: status %d, want 401 (it is not an ingest token)", resp.StatusCode)
	}
	resp, _ = do(t, srv, "POST", "/api/runs", token,
		map[string]any{"commit": "abc123", "report": map[string]any{"findings": []any{}}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing branch: status %d, want 400", resp.StatusCode)
	}
}

func TestTokenRegenerateAndRevoke(t *testing.T) {
	srv, _ := newTestServer(t)
	id, old := registerRepo(t, srv, "github.com/acme/widgets")
	run := map[string]any{"branch": "main", "commit": "c1", "report": map[string]any{"findings": []any{}}}

	resp, body := do(t, srv, "POST", "/api/admin/repos/"+itoa(id)+"/token", testAdminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("regenerate: status %d, body %s", resp.StatusCode, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	json.Unmarshal(body, &out)

	if resp, _ := do(t, srv, "POST", "/api/runs", old, run); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("old token after regenerate: status %d, want 401", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "POST", "/api/runs", out.Token, run); resp.StatusCode != http.StatusOK {
		t.Errorf("new token: status %d, want 200", resp.StatusCode)
	}

	if resp, _ := do(t, srv, "DELETE", "/api/admin/repos/"+itoa(id), testAdminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete: status %d, want 204", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "POST", "/api/runs", out.Token, run); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("token after repo delete: status %d, want 401", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "DELETE", "/api/admin/repos/9999", testAdminToken, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("delete missing repo: status %d, want 404", resp.StatusCode)
	}
}

func TestServesEmbeddedIndex(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, body := do(t, srv, "GET", "/", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: status %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte("<title>dash</title>")) {
		t.Errorf("GET / served %s, want the embedded index.html", body)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }
```

Add `"strconv"` to the test's imports.

- [ ] **Step 2: Add `Reset` to the store (needed by the test helper)**

In `internal/dashboard/store/store.go`:

```go
// Reset truncates every table. It exists for tests; production never calls it.
func (s *Store) Reset(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `TRUNCATE repos RESTART IDENTITY CASCADE`)
	return err
}
```

Then simplify Task 1's `testStore` helper to call `s.Reset(context.Background())` instead of reaching into `s.db` directly.

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/dashboard/api/ -v`
Expected: FAIL to build — `undefined: New`.

- [ ] **Step 4: Write `internal/dashboard/api/api.go`**

```go
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"

	"codebase-analyser/internal/dashboard/store"
)

// maxBodyBytes caps an ingest push. A run's report is measured in tens of
// kilobytes; this is the trust-boundary guard that stops an ingest token from
// being a memory-exhaustion lever.
const maxBodyBytes = 32 << 20 // 32 MiB

type server struct {
	st         *store.Store
	adminToken string
}

// New builds the whole HTTP surface: the JSON API plus the embedded SPA on /.
// assets must contain index.html at its root.
func New(st *store.Store, adminToken string, assets fs.FS) http.Handler {
	s := &server{st: st, adminToken: adminToken}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/admin/repos", s.admin(s.createRepo))
	mux.HandleFunc("GET /api/admin/repos", s.admin(s.listRepos))
	mux.HandleFunc("POST /api/admin/repos/{id}/token", s.admin(s.regenerateToken))
	mux.HandleFunc("DELETE /api/admin/repos/{id}", s.admin(s.deleteRepo))
	mux.HandleFunc("POST /api/runs", s.ingest)
	s.registerReadRoutes(mux) // Task 5

	mux.Handle("GET /", http.FileServerFS(assets))
	return mux
}

// admin gates a handler behind the deploy-time admin token. The comparison is
// constant-time: a timing oracle on a long-lived shared secret is worth the
// one-line defence.
func (s *server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if s.adminToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.adminToken)) != 1 {
			httpError(w, http.StatusUnauthorized, "invalid admin token")
			return
		}
		h(w, r)
	}
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // findings routinely contain "<-chan"
	if err := enc.Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// pathID reads a {id} path value, replying 404 for anything unparseable -
// a non-numeric id can only ever be a route that matches nothing.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusNotFound, "not found")
		return 0, false
	}
	return id, true
}

func (s *server) createRepo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RemoteURL string `json:"remote_url"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if strings.TrimSpace(req.RemoteURL) == "" {
		httpError(w, http.StatusBadRequest, "remote_url is required")
		return
	}
	repo, token, err := s.st.CreateRepo(r.Context(), req.RemoteURL)
	if err != nil {
		if strings.Contains(err.Error(), "already registered") {
			httpError(w, http.StatusConflict, err.Error())
			return
		}
		serverError(w, "create repo", err)
		return
	}
	// The only time the token is ever returned. It is not stored in plaintext,
	// so an admin who loses it must regenerate rather than look it up.
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": repo.ID, "remote_url": repo.RemoteURL, "token": token,
	})
}

func (s *server) listRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.st.ListRepos(r.Context())
	if err != nil {
		serverError(w, "list repos", err)
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

func (s *server) regenerateToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	token, err := s.st.RegenerateToken(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "repo not found")
		return
	}
	if err != nil {
		serverError(w, "regenerate token", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (s *server) deleteRepo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.st.DeleteRepo(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "repo not found")
		return
	}
	if err != nil {
		serverError(w, "delete repo", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ingestRequest is the CLI's push envelope: git metadata the CLI detects,
// the tool statuses from the orchestrator, and the unmodified JSON report.
type ingestRequest struct {
	Branch string             `json:"branch"`
	Commit string             `json:"commit"`
	Tools  []store.ToolStatus `json:"tools"`
	Report struct {
		Findings []store.Finding `json:"findings"`
	} `json:"report"`
}

// ingest is authenticated by the per-repo ingest token alone: the token IS
// the repo identity, so there is no repo id in the path that could disagree
// with it and no way for one repo's token to write another repo's history.
func (s *server) ingest(w http.ResponseWriter, r *http.Request) {
	repo, err := s.st.RepoByToken(r.Context(), bearer(r))
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusUnauthorized, "invalid ingest token")
		return
	}
	if err != nil {
		serverError(w, "resolve ingest token", err)
		return
	}

	var req ingestRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if req.Branch == "" || req.Commit == "" {
		httpError(w, http.StatusBadRequest, "branch and commit are required")
		return
	}

	// The pushed summary is deliberately ignored; SaveRun recounts from the
	// findings so stored counts can never contradict stored findings.
	runID, err := s.st.SaveRun(r.Context(), repo.ID, req.Branch, req.Commit, req.Tools, req.Report.Findings)
	if err != nil {
		serverError(w, "save run", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return err
	}
	return nil
}

// serverError logs the real cause and returns a generic message, so a SQL
// error never travels to a client.
func serverError(w http.ResponseWriter, what string, err error) {
	log.Printf("%s: %v", what, err)
	httpError(w, http.StatusInternalServerError, what+" failed")
}
```

- [ ] **Step 5: Add a temporary stub so the package compiles**

Task 5 owns `registerReadRoutes`. Add this stub in `api.go` now and replace it in Task 5:

```go
// registerReadRoutes is implemented in dashboard.go (Task 5).
func (s *server) registerReadRoutes(mux *http.ServeMux) {}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/dashboard/... -v -count=1`
Expected: PASS. The api package's 7 tests plus the store package's 10.

- [ ] **Step 7: Verify the module is clean**

Run: `go build ./... && go vet ./... && gofmt -l .`
Expected: no output.

- [ ] **Step 8: Hand back to the user**

Do NOT run git commands. Report the files created/modified and stop so the user can commit.

---

### Task 5: API — the dashboard payload, run drill-down, and a runnable binary

One read endpoint returns everything the page renders, so the frontend is a renderer with no assembly logic of its own. After this task `go run ./cmd/dashboard` serves a working API.

**Files:**
- Create: `internal/dashboard/api/dashboard.go`
- Test: `internal/dashboard/api/dashboard_test.go`
- Create: `internal/dashboard/web/embed.go`
- Create: `internal/dashboard/web/index.html` (placeholder; Tasks 7-9 replace it)
- Create: `cmd/dashboard/main.go`
- Modify: `internal/dashboard/api/api.go` (delete the `registerReadRoutes` stub from Task 4 Step 5)

**Interfaces:**
- Consumes: Tasks 1-4.
- Produces: `GET /api/repos/{id}/dashboard?branch=&limit=` and `GET /api/runs/{id}` (both admin-token gated), `web.Assets fs.FS`, and the JSON payload the frontend consumes:

```jsonc
{
  "repo":     {"id": 1, "remote_url": "github.com/acme/widgets", "registered_at": "..."},
  "branch":   "main",                       // the branch actually served
  "branches": [{"name", "last_run_at", "commit_sha", "run_id", "counts"}],
  "history":  [{"run_id", "commit_sha", "pushed_at", "counts", "health"}],  // oldest first
  "current":  {                             // null when the branch has no runs
    "run":        {"id", "repo_id", "branch", "commit_sha", "pushed_at", "counts", "tools"},
    "health":     72,
    "deltas":     {"critical": -1, "high": 0, "medium": 2, "low": 0},  // vs the previous run
    "new":        3,
    "fixed":      1,
    "categories": {"correctness": 1, "concurrency": 0, "security": 2, "operational": 0},
    "top_files":  [{"file": "a.go", "count": 4}],
    "findings":   [{"file", "line", "tool", "ruleID", "category", "severity", "message", "explanation"}]
  }
}
```

- [ ] **Step 1: Write the failing test**

`internal/dashboard/api/dashboard_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"codebase-analyser/internal/dashboard/store"
)

// pushRun sends one run through the real ingest endpoint, so these tests
// exercise the same path CI does.
func pushRun(t *testing.T, srv *httptestServer, token, branch, commit string, findings []store.Finding, tools []store.ToolStatus) {
	t.Helper()
	if findings == nil {
		findings = []store.Finding{}
	}
	body := map[string]any{
		"branch": branch, "commit": commit, "tools": tools,
		"report": map[string]any{"findings": findings},
	}
	resp, out := do(t, srv, "POST", "/api/runs", token, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("push %s@%s: status %d, body %s", branch, commit, resp.StatusCode, out)
	}
}

func sev(severity, file string, line int, rule string) store.Finding {
	return store.Finding{File: file, Line: line, Tool: "gosec", RuleID: rule,
		Category: "security", Severity: severity, Message: rule + " on " + file}
}

func getDashboard(t *testing.T, srv *httptestServer, repoID int64, query string) dashboardResponse {
	t.Helper()
	resp, body := do(t, srv, "GET", "/api/repos/"+strconv.FormatInt(repoID, 10)+"/dashboard"+query, testAdminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard: status %d, body %s", resp.StatusCode, body)
	}
	var out dashboardResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode dashboard: %v (body %s)", err, body)
	}
	return out
}

func TestDashboardDefaultsToMainAndAssemblesEveryView(t *testing.T) {
	srv, _ := newTestServer(t)
	repoID, token := registerRepo(t, srv, "github.com/acme/widgets")

	pushRun(t, srv, token, "feature", "f1", []store.Finding{sev("low", "z.go", 1, "G104")}, nil)
	pushRun(t, srv, token, "main", "c1", []store.Finding{
		sev("high", "a.go", 1, "G101"),
		sev("high", "a.go", 2, "G102"),
		sev("low", "b.go", 3, "G104"),
	}, nil)
	pushRun(t, srv, token, "main", "c2", []store.Finding{
		sev("high", "a.go", 1, "G101"),                      // carried over
		{File: "c.go", Line: 9, Tool: "clippy", RuleID: "unwrap_used", // new, other category
			Category: "correctness", Severity: "critical", Message: "unwrap"},
	}, []store.ToolStatus{{Name: "gosec"}, {Name: "clippy", Skipped: true, Error: "install failed"}})

	got := getDashboard(t, srv, repoID, "")

	if got.Branch != "main" {
		t.Errorf("served branch %q, want the default 'main'", got.Branch)
	}
	if len(got.Branches) != 2 {
		t.Errorf("branches = %+v, want both main and feature", got.Branches)
	}
	if len(got.History) != 2 || got.History[0].CommitSHA != "c1" {
		t.Fatalf("history = %+v, want c1 then c2 (oldest first)", got.History)
	}
	if got.Current == nil {
		t.Fatal("current is null on a branch that has runs")
	}
	if got.Current.Run.CommitSHA != "c2" {
		t.Errorf("current run = %s, want the latest c2", got.Current.Run.CommitSHA)
	}
	if got.Current.New != 1 || got.Current.Fixed != 2 {
		t.Errorf("new=%d fixed=%d, want new=1 (c.go) fixed=2 (a.go:2, b.go)", got.Current.New, got.Current.Fixed)
	}
	if got.Current.Deltas["critical"] != 1 || got.Current.Deltas["low"] != -1 {
		t.Errorf("deltas = %v, want critical +1 and low -1 vs c1", got.Current.Deltas)
	}
	if got.Current.Categories["security"] != 1 || got.Current.Categories["correctness"] != 1 {
		t.Errorf("categories = %v", got.Current.Categories)
	}
	if len(got.Current.TopFiles) == 0 || got.Current.TopFiles[0].Count != 1 {
		t.Errorf("top_files = %+v", got.Current.TopFiles)
	}
	if len(got.Current.Findings) != 2 {
		t.Errorf("findings = %d, want 2", len(got.Current.Findings))
	}
	if len(got.Current.Run.Tools) != 2 || !got.Current.Run.Tools[1].Skipped {
		t.Errorf("tool statuses = %+v, want clippy marked skipped", got.Current.Run.Tools)
	}
	if got.Current.Health <= 0 || got.Current.Health > 100 {
		t.Errorf("health = %d, want a 1..100 score", got.Current.Health)
	}
}

func TestDashboardHonoursBranchQuery(t *testing.T) {
	srv, _ := newTestServer(t)
	repoID, token := registerRepo(t, srv, "github.com/acme/widgets")
	pushRun(t, srv, token, "main", "c1", []store.Finding{sev("high", "a.go", 1, "G101")}, nil)
	pushRun(t, srv, token, "feature", "f1", []store.Finding{sev("low", "z.go", 1, "G104")}, nil)

	got := getDashboard(t, srv, repoID, "?branch=feature")
	if got.Branch != "feature" || got.Current == nil || got.Current.Run.CommitSHA != "f1" {
		t.Fatalf("branch query ignored: %+v", got)
	}
	if got.Current.Run.Counts["low"] != 1 || got.Current.Run.Counts["high"] != 0 {
		t.Errorf("counts = %v, want only the feature branch's findings", got.Current.Run.Counts)
	}
}

func TestDashboardEmptyAndMissingRepo(t *testing.T) {
	srv, _ := newTestServer(t)
	repoID, _ := registerRepo(t, srv, "github.com/acme/widgets")

	got := getDashboard(t, srv, repoID, "")
	if got.Current != nil || len(got.Branches) != 0 || len(got.History) != 0 {
		t.Errorf("a repo with no runs returned %+v, want empty views and a null current", got)
	}

	resp, _ := do(t, srv, "GET", "/api/repos/9999/dashboard", testAdminToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing repo: status %d, want 404", resp.StatusCode)
	}
	resp, _ = do(t, srv, "GET", "/api/repos/"+strconv.FormatInt(repoID, 10)+"/dashboard", "wrong", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad admin token: status %d, want 401", resp.StatusCode)
	}
}

func TestRunDrillDown(t *testing.T) {
	srv, _ := newTestServer(t)
	repoID, token := registerRepo(t, srv, "github.com/acme/widgets")
	pushRun(t, srv, token, "main", "c1", []store.Finding{sev("high", "a.go", 7, "G101")}, nil)
	pushRun(t, srv, token, "main", "c2", nil, nil)

	dash := getDashboard(t, srv, repoID, "")
	older := dash.History[0].RunID

	resp, body := do(t, srv, "GET", "/api/runs/"+strconv.FormatInt(older, 10), testAdminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("run detail: status %d, body %s", resp.StatusCode, body)
	}
	var out runResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Run.CommitSHA != "c1" || len(out.Findings) != 1 || out.Findings[0].Line != 7 {
		t.Errorf("drill-down = %+v", out)
	}

	resp, _ = do(t, srv, "GET", "/api/runs/9999", testAdminToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing run: status %d, want 404", resp.StatusCode)
	}
}
```

Add `type httptestServer = httptest.Server` to `api_test.go` (with the `net/http/httptest` import already there) so the helper signatures above compile against the Task 4 helpers unchanged.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/dashboard/api/ -v`
Expected: FAIL to build — `undefined: dashboardResponse`, `undefined: runResponse`.

- [ ] **Step 3: Write `internal/dashboard/api/dashboard.go`**

```go
package api

import (
	"errors"
	"net/http"
	"strconv"

	"codebase-analyser/internal/dashboard/store"
)

// defaultHistoryLimit is how many runs the trend chart plots. Enough to show
// a real trend, few enough that the payload stays small; ?limit= overrides.
const defaultHistoryLimit = 30

// topFileCount is how many rows the "top offending files" view shows.
const topFileCount = 10

type historyPoint struct {
	RunID     int64          `json:"run_id"`
	CommitSHA string         `json:"commit_sha"`
	PushedAt  string         `json:"pushed_at"`
	Counts    map[string]int `json:"counts"`
	Health    int            `json:"health"`
}

type currentRun struct {
	Run        store.Run       `json:"run"`
	Health     int             `json:"health"`
	Deltas     map[string]int  `json:"deltas"`
	New        int             `json:"new"`
	Fixed      int             `json:"fixed"`
	Categories map[string]int  `json:"categories"`
	TopFiles   []FileCount     `json:"top_files"`
	Findings   []store.Finding `json:"findings"`
}

type dashboardResponse struct {
	Repo     store.Repo     `json:"repo"`
	Branch   string         `json:"branch"`
	Branches []store.Branch `json:"branches"`
	History  []historyPoint `json:"history"`
	Current  *currentRun    `json:"current"`
}

type runResponse struct {
	Run      store.Run       `json:"run"`
	Findings []store.Finding `json:"findings"`
}

func (s *server) registerReadRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/repos/{id}/dashboard", s.admin(s.dashboard))
	mux.HandleFunc("GET /api/runs/{id}", s.admin(s.runDetail))
}

// dashboard assembles every view on the page in one response. The frontend is
// then a pure renderer: it never has to decide which run is current, which
// branch to show, or how to derive a number.
func (s *server) dashboard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	repo, err := s.st.RepoByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "repo not found")
		return
	}
	if err != nil {
		serverError(w, "get repo", err)
		return
	}

	branches, err := s.st.Branches(ctx, repo.ID)
	if err != nil {
		serverError(w, "list branches", err)
		return
	}

	branch := r.URL.Query().Get("branch")
	if branch == "" {
		branch = PickDefaultBranch(branches)
	}
	resp := dashboardResponse{Repo: repo, Branch: branch, Branches: branches, History: []historyPoint{}}
	if branch == "" { // repo registered but nothing pushed yet
		writeJSON(w, http.StatusOK, resp)
		return
	}

	limit := defaultHistoryLimit
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	runs, err := s.st.RunsForBranch(ctx, repo.ID, branch, limit)
	if err != nil {
		serverError(w, "list runs", err)
		return
	}
	if len(runs) == 0 {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// History carries a health score per point so the trend chart and the
	// health view are reading the same series.
	for i, run := range runs {
		var prev map[string]int
		if i > 0 {
			prev = runs[i-1].Counts
		}
		resp.History = append(resp.History, historyPoint{
			RunID:     run.ID,
			CommitSHA: run.CommitSHA,
			PushedAt:  run.PushedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Counts:    run.Counts,
			Health:    HealthScore(run.Counts, prev),
		})
	}

	current := runs[len(runs)-1]
	findings, err := s.st.FindingsForRun(ctx, current.ID)
	if err != nil {
		serverError(w, "list findings", err)
		return
	}

	// "Since last run" compares the two most recent runs on this branch. With
	// only one run everything is new and nothing is fixed, which Diff already
	// yields for a nil previous.
	var prevCounts map[string]int
	var prevFindings []store.Finding
	if len(runs) > 1 {
		previous := runs[len(runs)-2]
		prevCounts = previous.Counts
		if prevFindings, err = s.st.FindingsForRun(ctx, previous.ID); err != nil {
			serverError(w, "list previous findings", err)
			return
		}
	}
	added, fixed := Diff(prevFindings, findings)

	resp.Current = &currentRun{
		Run:        current,
		Health:     HealthScore(current.Counts, prevCounts),
		Deltas:     deltas(current.Counts, prevCounts),
		New:        added,
		Fixed:      fixed,
		Categories: CategoryCounts(findings),
		TopFiles:   TopFiles(findings, topFileCount),
		Findings:   findings,
	}
	writeJSON(w, http.StatusOK, resp)
}

// deltas is the per-severity change the summary cards show. A first run has
// no previous, and reports every count as unchanged rather than as a jump
// from zero, which would read as a regression on a brand-new branch.
func deltas(cur, prev map[string]int) map[string]int {
	out := map[string]int{}
	for sev := range severityWeight {
		out[sev] = 0
		if prev != nil {
			out[sev] = cur[sev] - prev[sev]
		}
	}
	return out
}

func (s *server) runDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, err := s.st.RunByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		serverError(w, "get run", err)
		return
	}
	findings, err := s.st.FindingsForRun(r.Context(), run.ID)
	if err != nil {
		serverError(w, "list findings", err)
		return
	}
	writeJSON(w, http.StatusOK, runResponse{Run: run, Findings: findings})
}
```

- [ ] **Step 4: Delete the Task 4 stub**

Remove the placeholder `func (s *server) registerReadRoutes(mux *http.ServeMux) {}` from `api.go` — the real one now lives in `dashboard.go`.

- [ ] **Step 5: Write `internal/dashboard/web/embed.go` and a placeholder page**

`internal/dashboard/web/embed.go`:

```go
// Package web carries the dashboard's frontend, compiled into the binary so a
// deployment is one file plus a Postgres URL.
package web

import "embed"

//go:embed index.html
var files embed.FS

// Assets is the frontend's root filesystem, served at /.
var Assets = files
```

`internal/dashboard/web/index.html` (Tasks 7-9 replace this wholesale):

```html
<!doctype html>
<meta charset="utf-8">
<title>Codebase Analyser</title>
<p>Dashboard frontend lands in Task 7.</p>
```

Note for Tasks 7-9: once `style.css` and `app.js` exist, the embed directive becomes `//go:embed index.html style.css app.js`.

- [ ] **Step 6: Write `cmd/dashboard/main.go`**

```go
// Command dashboard serves the codebase-analyser web dashboard: an HTTP API
// that ingests CLI run results plus the embedded single-page frontend.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"codebase-analyser/internal/dashboard/api"
	"codebase-analyser/internal/dashboard/store"
	"codebase-analyser/internal/dashboard/web"
)

func main() {
	// Config is env-only: this runs from docker-compose, where env is the
	// native way to pass secrets, and there is nothing here a flag would
	// serve better.
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required (e.g. postgres://user:pass@db:5432/analyser?sslmode=disable)")
	}
	adminToken := os.Getenv("DASHBOARD_ADMIN_TOKEN")
	if adminToken == "" {
		log.Fatal("DASHBOARD_ADMIN_TOKEN is required; it gates the UI and all repo management")
	}
	addr := os.Getenv("DASHBOARD_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer st.Close()

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(st, adminToken, web.Assets),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("dashboard listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/dashboard/... -v -count=1`
Expected: PASS — 21 tests across the two packages.

- [ ] **Step 8: Drive the real binary end to end**

```bash
DATABASE_URL="$DASHBOARD_TEST_DSN" DASHBOARD_ADMIN_TOKEN=dev go run ./cmd/dashboard &
sleep 2
curl -s -X POST localhost:8080/api/admin/repos -H 'Authorization: Bearer dev' \
  -d '{"remote_url":"github.com/acme/widgets"}'
# copy the returned token into $T, then:
curl -s -X POST localhost:8080/api/runs -H "Authorization: Bearer $T" \
  -d '{"branch":"main","commit":"c1","tools":[{"name":"gosec"}],"report":{"findings":[{"file":"a.go","line":1,"tool":"gosec","ruleID":"G101","category":"security","severity":"high","message":"m","explanation":"e"}]}}'
curl -s localhost:8080/api/repos/1/dashboard -H 'Authorization: Bearer dev'
curl -s localhost:8080/ | head -3
kill %1
```

Expected: the register call returns a token, ingest returns `{"run_id":1}`, the dashboard call returns a payload whose `current.run.counts.high` is 1, and `/` serves the placeholder page.

- [ ] **Step 9: Verify the module is clean**

Run: `go build ./... && go vet ./... && gofmt -l .`
Expected: no output.

- [ ] **Step 10: Hand back to the user**

Do NOT run git commands. Report the files created/modified and stop so the user can commit.

---

### Task 6: CLI — best-effort push to the dashboard

**Files:**
- Create: `internal/push/push.go`
- Test: `internal/push/push_test.go`
- Modify: `internal/cli/run.go` (new flags, new `RunConfig` fields, push call in `Execute`)
- Test: `internal/cli/run_test.go` (append; do not rewrite the existing tests)

**Interfaces:**
- Consumes: the ingest wire format from Task 4; `orchestrator.ToolResult`; `report.RenderJSON`.
- Produces: `push.GitMeta(dir string) (remote, branch, commit string, err error)`, `push.ToolStatus{Name string; Skipped bool; Error string}`, `push.Send(ctx context.Context, baseURL, token, branch, commit string, tools []ToolStatus, reportJSON []byte) error`, `cli.RunConfig.DashboardURL`, `cli.RunConfig.DashboardToken`.

**Constraint restated:** the push must never change the exit code or fail the run. A dashboard outage is a warning on stderr and nothing else.

> **⚠️ The code quoted in Step 7 is stale — re-read `internal/cli/run.go` before editing it.** A concurrent session rewrote much of the CLI after this plan was written. Verified current signatures, as of Task 6's dispatch:
>
> - `detect.Detect(path)` returns **three** values: `(projects, skippedPaths []string, err)`.
> - `orchestrator.ToolResult` has a **`Path`** field, so the same tool appears once per project.
> - `report.RenderJSON(w, findings, skipped []report.SkippedTool) error` and `report.RenderHuman(w, findings, skipped)` both take a **third argument**. `report.SkippedTool` is `{Tool, Path, Reason string}`.
> - `computeExitCode(explained, threshold, incompleteCoverage bool)` — exit **2** means "analysis incomplete" (a tool was skipped), distinct from 1 for "findings at or above threshold".
> - The JSON report itself gained top-level `incomplete` and `skippedTools` fields, and each finding gained `fixPattern`.
>
> **Consequences for this task:** `pushRun` must pass the run's `[]report.SkippedTool` to `RenderJSON` as well as the findings. The dashboard's ingest decodes only `report.findings` and ignores unknown keys, so the added report fields need no server change. `toolStatuses` is still required and is NOT redundant with the report's new `skippedTools` — that lists only the tools that failed, whereas the dashboard's tool-status view must also show which tools ran successfully. Treat it as intent, not as text to paste. Specifically:
> - Validate `--dashboard-url`/`--dashboard-token` **alongside the other flags, before the pipeline runs**, so a bad token fails in milliseconds rather than after minutes of linting.
> - Emit the push-failure warning through the existing `writeNotes` helper rather than writing to `os.Stderr` directly, so `--format json` keeps stdout pure.
> - **A failed push must NOT produce exit code 2.** Exit 2 means "analysis incomplete" — a tool was skipped, so the findings are not trustworthy. A dashboard outage says nothing about the analysis: the findings are complete and the exit code must remain whatever the analysis alone produced (0 or 1). This is the spec's "a dashboard outage never fails a CI run or overrides the CLI's own severity-based exit code", and it now has a third code it must also stay clear of.
>
> Ping session `codebase-analyser-b5` before editing this file.

**Note on git:** this task shells out to `git` for **read-only** queries (`config --get`, `rev-parse`) only. The standing no-git-operations rule still applies to the implementer's own shell — do not run `git init`/`add`/`commit` anywhere, including in tests.

- [ ] **Step 1: Write the failing test**

`internal/push/push_test.go`:

```go
package push

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSendPostsTheIngestEnvelope(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Write([]byte(`{"run_id":1}`))
	}))
	defer srv.Close()

	report := []byte(`{"summary":{"critical":0,"high":1,"medium":0,"low":0},"findings":[{"file":"a.go","line":1,"tool":"gosec","ruleID":"G101","category":"security","severity":"high","message":"m","explanation":"e"}]}`)
	tools := []ToolStatus{{Name: "gosec"}, {Name: "clippy", Skipped: true, Error: "install failed"}}

	if err := Send(context.Background(), srv.URL, "tok", "main", "abc123", tools, report); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/api/runs" {
		t.Errorf("posted to %q, want /api/runs", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q, want Bearer tok", gotAuth)
	}
	if gotBody["branch"] != "main" || gotBody["commit"] != "abc123" {
		t.Errorf("envelope metadata = %v", gotBody)
	}
	// The report must be nested as an object, not re-encoded as a string.
	rep, ok := gotBody["report"].(map[string]any)
	if !ok {
		t.Fatalf("report field = %T, want a JSON object", gotBody["report"])
	}
	findings, _ := rep["findings"].([]any)
	if len(findings) != 1 {
		t.Errorf("report.findings = %v, want the CLI report verbatim", rep["findings"])
	}
	toolList, _ := gotBody["tools"].([]any)
	if len(toolList) != 2 {
		t.Errorf("tools = %v, want both statuses", gotBody["tools"])
	}
}

func TestSendTrimsTrailingSlashOnBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	}))
	defer srv.Close()
	if err := Send(context.Background(), srv.URL+"/", "tok", "main", "c1", nil, []byte(`{}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/api/runs" {
		t.Errorf("path = %q, want /api/runs (no double slash)", gotPath)
	}
}

func TestSendSurfacesServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid ingest token"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := Send(context.Background(), srv.URL, "bad", "main", "c1", nil, []byte(`{}`))
	if err == nil {
		t.Fatal("Send returned nil for a 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want the status code in the message", err)
	}
}

func TestSendFailsFastWhenUnreachable(t *testing.T) {
	// Nothing is listening; the caller treats this as a warning, so all that
	// matters is that it returns an error promptly rather than hanging.
	start := time.Now()
	err := Send(context.Background(), "http://127.0.0.1:1", "tok", "main", "c1", nil, []byte(`{}`))
	if err == nil {
		t.Fatal("Send returned nil against a dead endpoint")
	}
	if time.Since(start) > 20*time.Second {
		t.Errorf("Send took %s; it must not hang a CI run", time.Since(start))
	}
}

func TestGitMetaReadsThisCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	remote, branch, commit, err := GitMeta("..")
	if err != nil {
		t.Skipf("not a git checkout with a remote: %v", err)
	}
	if len(commit) < 7 {
		t.Errorf("commit = %q, want a full sha", commit)
	}
	if branch == "" {
		t.Error("branch is empty")
	}
	if remote == "" {
		t.Error("remote is empty")
	}
}

func TestGitMetaFailsOutsideARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, _, _, err := GitMeta(t.TempDir()); err == nil {
		t.Error("GitMeta succeeded outside a git repository, want an error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/push/ -v`
Expected: FAIL to build — `undefined: Send`, `undefined: GitMeta`, `undefined: ToolStatus`.

- [ ] **Step 3: Write `internal/push/push.go`**

```go
// Package push sends a completed CLI run to a self-hosted dashboard. Every
// failure here is a warning, never an error that reaches the user's exit
// code: a dashboard outage must not fail a CI build.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// timeout bounds the whole push. CI runs wait on this, so it is short.
const timeout = 15 * time.Second

type ToolStatus struct {
	Name    string `json:"name"`
	Skipped bool   `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

// envelope is the ingest wire format. Report is json.RawMessage so the CLI's
// report is embedded as an object exactly as rendered, never re-encoded.
type envelope struct {
	Branch string          `json:"branch"`
	Commit string          `json:"commit"`
	Tools  []ToolStatus    `json:"tools"`
	Report json.RawMessage `json:"report"`
}

// Send posts one run to the dashboard's ingest endpoint.
func Send(ctx context.Context, baseURL, token, branch, commit string, tools []ToolStatus, reportJSON []byte) error {
	if tools == nil {
		tools = []ToolStatus{}
	}
	body, err := json.Marshal(envelope{
		Branch: branch, Commit: commit, Tools: tools, Report: reportJSON,
	})
	if err != nil {
		return fmt.Errorf("encode push payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	url := strings.TrimSuffix(baseURL, "/") + "/api/runs"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("push to %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("dashboard returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// GitMeta reads the repo identity the dashboard keys runs on, straight out of
// the local checkout. All three commands are read-only.
func GitMeta(dir string) (remote, branch, commit string, err error) {
	if remote, err = git(dir, "config", "--get", "remote.origin.url"); err != nil {
		return "", "", "", fmt.Errorf("read git remote: %w", err)
	}
	if branch, err = git(dir, "rev-parse", "--abbrev-ref", "HEAD"); err != nil {
		return "", "", "", fmt.Errorf("read git branch: %w", err)
	}
	if commit, err = git(dir, "rev-parse", "HEAD"); err != nil {
		return "", "", "", fmt.Errorf("read git commit: %w", err)
	}
	return remote, branch, commit, nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", fmt.Errorf("git %s returned nothing", strings.Join(args, " "))
	}
	return value, nil
}
```

- [ ] **Step 4: Run the push tests to verify they pass**

Run: `go test ./internal/push/ -v -count=1`
Expected: PASS, 6 tests.

- [ ] **Step 5: Write the failing CLI test**

Append to `internal/cli/run_test.go`:

```go
func TestToolStatusesDedupesAcrossProjects(t *testing.T) {
	// A repo with two Go projects runs each adapter twice. The dashboard
	// wants one row per tool, and a tool that was skipped anywhere must not
	// be reported as having run cleanly.
	results := []orchestrator.ToolResult{
		{Tool: "gosec"},
		{Tool: "gosec", Skipped: true, Error: errors.New("install failed")},
		{Tool: "golangci-lint"},
	}
	got := toolStatuses(results)
	if len(got) != 2 {
		t.Fatalf("got %d statuses, want one per distinct tool: %+v", len(got), got)
	}
	byName := map[string]push.ToolStatus{}
	for _, s := range got {
		byName[s.Name] = s
	}
	if !byName["gosec"].Skipped || byName["gosec"].Error != "install failed" {
		t.Errorf("gosec = %+v, want skipped with its reason", byName["gosec"])
	}
	if byName["golangci-lint"].Skipped {
		t.Errorf("golangci-lint = %+v, want not skipped", byName["golangci-lint"])
	}
	// Order must be stable so a re-push does not churn the stored JSON.
	if got[0].Name != "golangci-lint" || got[1].Name != "gosec" {
		t.Errorf("order = %s,%s, want alphabetical", got[0].Name, got[1].Name)
	}
}

func TestPushRequiresBothFlags(t *testing.T) {
	cmd := NewRunCmd()
	cmd.SetArgs([]string{t.TempDir(), "--dashboard-url", "http://localhost:9999"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--dashboard-token") {
		t.Errorf("err = %v, want a complaint that --dashboard-token is missing", err)
	}
}
```

Add `errors`, `io`, `strings`, `codebase-analyser/internal/orchestrator` and `codebase-analyser/internal/push` to the test file's imports as needed.

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./internal/cli/ -run 'TestToolStatuses|TestPushRequires' -v`
Expected: FAIL — `undefined: toolStatuses`, and the flag test fails because `--dashboard-url` does not exist yet.

- [ ] **Step 7: Wire the push into `internal/cli/run.go`**

Add to the imports: `"bytes"`, `"sort"`, `"codebase-analyser/internal/push"`.

Add two fields to `RunConfig`:

```go
type RunConfig struct {
	Path           string
	Format         string
	Severity       finding.Severity
	Categories     []finding.Category
	LLMProvider    string
	NoLLM          bool
	DashboardURL   string
	DashboardToken string
}
```

Register the flags in `NewRunCmd`, next to the existing ones:

```go
	cmd.Flags().StringVar(&cfg.DashboardURL, "dashboard-url", "", "push this run to a dashboard at this base URL")
	cmd.Flags().StringVar(&cfg.DashboardToken, "dashboard-token", os.Getenv("ANALYSER_DASHBOARD_TOKEN"), "ingest token for --dashboard-url (default $ANALYSER_DASHBOARD_TOKEN)")
```

Validate them in `RunE`, alongside the existing `--format` check:

```go
			if cfg.DashboardURL != "" && cfg.DashboardToken == "" {
				return fmt.Errorf("--dashboard-url needs --dashboard-token (or $ANALYSER_DASHBOARD_TOKEN)")
			}
```

In `Execute`, capture the tool results before the loop consumes them, and push after the report is written. Replace the tail of `Execute` (from `writeNotes` onward) with:

```go
	writeNotes(w, cfg.Format, notes)

	if cfg.Format == "json" {
		if err := report.RenderJSON(w, explained); err != nil {
			return 1, err
		}
	} else {
		report.RenderHuman(w, explained)
	}

	// The push is the last thing that happens and is deliberately unable to
	// affect the outcome: its failures are warnings on stderr, and the exit
	// code below is computed from the findings either way.
	if cfg.DashboardURL != "" {
		if err := pushRun(ctx, cfg, results, explained); err != nil {
			fmt.Fprintf(os.Stderr, "warning: dashboard push failed: %v\n", err)
		}
	}

	return computeExitCode(explained, cfg.Severity), nil
}

// pushRun re-renders the report as JSON (regardless of --format, since the
// dashboard's wire format is the JSON report) and sends it with git metadata
// read from the analysed checkout.
func pushRun(ctx context.Context, cfg RunConfig, results []orchestrator.ToolResult, explained []finding.ExplainedFinding) error {
	remote, branch, commit, err := push.GitMeta(cfg.Path)
	if err != nil {
		return fmt.Errorf("%s is not a git checkout with an origin remote: %w", cfg.Path, err)
	}
	_ = remote // the ingest token identifies the repo; remote is read to prove this is a real checkout

	var buf bytes.Buffer
	if err := report.RenderJSON(&buf, explained); err != nil {
		return fmt.Errorf("render report for push: %w", err)
	}
	return push.Send(ctx, cfg.DashboardURL, cfg.DashboardToken, branch, commit, toolStatuses(results), buf.Bytes())
}

// toolStatuses collapses per-project results into one row per tool, which is
// what the dashboard's tool-status view shows. A tool skipped for any project
// is reported as skipped: "it ran somewhere" would hide exactly the missing
// coverage the view exists to surface. Sorted so a re-push of the same commit
// stores byte-identical JSON.
func toolStatuses(results []orchestrator.ToolResult) []push.ToolStatus {
	byName := map[string]push.ToolStatus{}
	for _, r := range results {
		existing, seen := byName[r.Tool]
		if seen && existing.Skipped {
			continue
		}
		status := push.ToolStatus{Name: r.Tool, Skipped: r.Skipped}
		if r.Error != nil {
			status.Error = r.Error.Error()
		}
		byName[r.Tool] = status
	}
	out := make([]push.ToolStatus, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
```

- [ ] **Step 8: Run the full suite**

Run: `go test ./... -count=1`
Expected: PASS in every package. The pre-existing `internal/cli` tests must still pass unchanged.

- [ ] **Step 9: Prove the push cannot break a run**

With the dashboard **not** running:

```bash
go run ./cmd/analyser run . --no-llm --severity critical \
  --dashboard-url http://127.0.0.1:1 --dashboard-token bogus
echo "exit=$?"
```

Expected: the normal human report prints, a single `warning: dashboard push failed: ...` line appears on stderr, and `exit=0` — the unreachable dashboard changed nothing.

Then with the Task 5 binary running and a real token in `$T`:

```bash
go run ./cmd/analyser run . --no-llm --dashboard-url http://localhost:8080 --dashboard-token "$T"
curl -s localhost:8080/api/repos/1/dashboard -H 'Authorization: Bearer dev' | head -40
```

Expected: no warning, and the dashboard payload now shows this repo's real branch, commit and findings.

- [ ] **Step 10: Verify the module is clean**

Run: `go build ./... && go vet ./... && gofmt -l .`
Expected: no output.

- [ ] **Step 11: Hand back to the user**

Do NOT run git commands. Report the files created/modified and stop so the user can commit.

---

### Task 7: Frontend scaffold — Vite/React/TS, design tokens, auth gate, top bar, branches, severity cards

Three tasks build the SPA. This one stands up the toolchain, the embed wiring, the validated color tokens and the page shell, so Tasks 8-9 only add components.

**Files:**
- Create: `internal/dashboard/web/ui/{package.json,vite.config.ts,tsconfig.json,index.html}`
- Create: `internal/dashboard/web/ui/src/{main.tsx,types.ts,api.ts,theme.ts,styles.css,App.tsx}`
- Create: `internal/dashboard/web/ui/src/components/{TopBar,BranchTable,SeverityCards,StackedSeverityBar}.tsx`
- Create: `internal/dashboard/web/ui/src/components/__tests__/{BranchTable,SeverityCards}.test.tsx`
- Create: `internal/dashboard/web/ui/src/test/fixtures.ts`
- Replace: `internal/dashboard/web/embed.go`
- Delete: `internal/dashboard/web/index.html` (the Task 5 placeholder)
- Test: `internal/dashboard/web/embed_test.go`

**Interfaces:**
- Consumes: `GET /api/admin/repos`, `GET /api/repos/{id}/dashboard?branch=` from Tasks 4-5.
- Produces (Tasks 8-9 import these): `types.ts` (`DashboardData`, `Branch`, `HistoryPoint`, `CurrentRun`, `Finding`, `ToolStatus`, `Severity`), `api.ts` (`fetchJSON<T>(path, token)`, `ApiError`), `theme.ts` (`SEVERITY_COLOR`, `SEVERITIES`, `STATUS`, `CHART`, `healthStatus`), and the `App.tsx` props each view receives.

- [ ] **Step 1: Scaffold the Vite project**

```bash
cd internal/dashboard/web/ui
npm init -y
npm install react react-dom recharts
npm install -D vite @vitejs/plugin-react typescript @types/react @types/react-dom \
  vitest jsdom @testing-library/react @testing-library/user-event @testing-library/jest-dom
```

Then replace the generated `package.json` scripts block with:

```json
  "scripts": {
    "dev": "vite",
    "build": "tsc --noEmit && vite build",
    "test": "vitest run"
  },
```

- [ ] **Step 2: Write `vite.config.ts`**

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  // The Go binary embeds ../dist, so that is where the build must land.
  build: { outDir: '../dist', emptyOutDir: true },
  // Relative asset URLs keep the bundle servable from any mount point.
  base: './',
  // `npm run dev` talks to a locally running dashboard binary.
  server: { proxy: { '/api': 'http://localhost:8080' } },
  test: { environment: 'jsdom', globals: true, setupFiles: './src/test/setup.ts' },
})
```

`tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noEmit": true,
    "types": ["vitest/globals", "@testing-library/jest-dom"]
  },
  "include": ["src"]
}
```

`src/test/setup.ts`:

```ts
import '@testing-library/jest-dom/vitest'
```

`index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Codebase Analyser</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

`src/main.tsx`:

```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './styles.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
```

- [ ] **Step 3: Write `src/types.ts`**

Mirrors the Go payload exactly — every field name here must match a JSON tag in `internal/dashboard/{store,api}`.

```ts
export type Severity = 'critical' | 'high' | 'medium' | 'low'
export type Counts = Record<Severity, number>

export interface Repo { id: number; remote_url: string; registered_at: string }

export interface Branch {
  name: string
  last_run_at: string
  commit_sha: string
  run_id: number
  counts: Counts
}

export interface ToolStatus { name: string; skipped: boolean; error?: string }

export interface Run {
  id: number
  repo_id: number
  branch: string
  commit_sha: string
  pushed_at: string
  counts: Counts
  tools: ToolStatus[]
}

export interface Finding {
  file: string
  line: number
  tool: string
  ruleID: string
  category: string
  severity: Severity
  message: string
  explanation: string
}

export interface HistoryPoint {
  run_id: number
  commit_sha: string
  pushed_at: string
  counts: Counts
  health: number
  new: number
  fixed: number
}

export interface FileCount { file: string; count: number }

export interface CurrentRun {
  run: Run
  health: number
  deltas: Counts
  new: number
  fixed: number
  categories: Record<string, number>
  top_files: FileCount[]
  findings: Finding[]
}

export interface DashboardData {
  repo: Repo
  branch: string
  branches: Branch[]
  history: HistoryPoint[]
  current: CurrentRun | null
}

export interface RunDetail { run: Run; findings: Finding[] }
```

- [ ] **Step 4: Write `src/api.ts`**

```ts
export class ApiError extends Error {
  constructor(message: string, readonly status: number) {
    super(message)
  }
}

export async function fetchJSON<T>(path: string, token: string): Promise<T> {
  const resp = await fetch(path, { headers: { Authorization: `Bearer ${token}` } })
  if (!resp.ok) {
    throw new ApiError(`${path} returned ${resp.status}`, resp.status)
  }
  return (await resp.json()) as T
}
```

- [ ] **Step 5: Write `src/theme.ts`**

Every value here came out of the dataviz validator. Do not substitute hexes by eye.

```ts
import type { Severity } from './types'

export const SEVERITIES: Severity[] = ['critical', 'high', 'medium', 'low']

/**
 * Severity is an ORDERED scale, so it is encoded with a single-hue ordinal
 * ramp rather than four categorical hues. The obvious red/orange/yellow/green
 * set fails CVD separation (worst pair ΔE 4.1 deutan, target >= 8) and the
 * normal-vision floor (13.6, hard floor 15) - it is unreadable to a
 * deuteranope. This ramp passes every ordinal check against the #1a1a19
 * surface: monotone lightness, adjacent ΔL >= 0.06, light end 2.15:1.
 */
export const SEVERITY_COLOR: Record<Severity, string> = {
  critical: '#b7d3f6',
  high: '#6da7ec',
  medium: '#2a78d6',
  low: '#184f95',
}

/**
 * Status hues are reserved for a LONE status mark that also carries a text
 * label - the health tile, the branch health pill, a delta arrow. Never four
 * of them in one chart.
 */
export const STATUS = {
  good: '#0ca30c',
  warning: '#fab219',
  serious: '#ec835a',
  critical: '#d03b3b',
} as const

export const CHART = {
  surface: '#1a1a19',
  series1: '#3987e5',   // categorical slot 1: single-series bars
  grid: '#2c2c2a',
  axis: '#383835',
  muted: '#898781',
  text: '#ffffff',
} as const

export function healthStatus(score: number): { color: string; label: string } {
  if (score >= 80) return { color: STATUS.good, label: 'healthy' }
  if (score >= 50) return { color: STATUS.warning, label: 'needs attention' }
  return { color: STATUS.critical, label: 'at risk' }
}

export function relativeTime(iso: string): string {
  const mins = Math.round((Date.now() - new Date(iso).getTime()) / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  if (mins < 60 * 24) return `${Math.round(mins / 60)}h ago`
  return `${Math.round(mins / 1440)}d ago`
}
```

- [ ] **Step 6: Write `src/styles.css`**

```css
/* Two easing curves for the whole page: --ease for anything data-driven,
   --spring for hover feedback. Ad hoc per-element curves are what make a
   page feel arbitrary. */
:root {
  --ease: cubic-bezier(.22, .61, .36, 1);
  --spring: cubic-bezier(.34, 1.56, .64, 1);

  --plane: #0d0d0d;          /* page plane */
  --surface: #1a1a19;        /* chart surface - what the validator was run against */
  --panel: rgba(26, 26, 25, .92);
  --stroke: rgba(255, 255, 255, .10);
  --text: #ffffff;
  --text-2: #c3c2b7;
  --muted: #898781;
  --grid: #2c2c2a;
  --accent: #3987e5;
}

* { box-sizing: border-box; }

body {
  margin: 0;
  padding: 24px;
  font: 15px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif;
  background: var(--plane);
  color: var(--text);
}

#root { display: flex; flex-direction: column; gap: 18px; }

/* Softly drifting gradient behind the panels. The panels themselves are
   near-opaque: chart contrast was validated against #1a1a19, and a fully
   translucent surface would make that number a lie. */
.bg {
  position: fixed;
  inset: -20%;
  z-index: -1;
  background:
    radial-gradient(45% 45% at 20% 25%, rgba(57, 135, 229, .22), transparent 70%),
    radial-gradient(40% 40% at 82% 18%, rgba(208, 59, 59, .14), transparent 70%),
    radial-gradient(50% 50% at 60% 85%, rgba(25, 158, 112, .12), transparent 70%);
  filter: blur(40px);
  animation: drift 26s var(--ease) infinite alternate;
}
@keyframes drift {
  from { transform: translate3d(-3%, -2%, 0) scale(1); }
  to   { transform: translate3d(3%, 3%, 0) scale(1.08); }
}

.panel {
  background: var(--panel);
  border: 1px solid var(--stroke);
  border-radius: 14px;
  padding: 18px 20px;
  backdrop-filter: blur(14px);
  animation: rise .5s var(--ease) both;
  animation-delay: calc(var(--i, 0) * 60ms);
}
@keyframes rise {
  from { opacity: 0; transform: translateY(14px); }
  to   { opacity: 1; transform: none; }
}
@media (prefers-reduced-motion: reduce) {
  .panel, .bg, .row, .card { animation: none; }
  * { transition: none !important; }
}

h1 { font-size: 20px; margin: 0 0 4px; }
h2 { font-size: 13px; letter-spacing: .09em; text-transform: uppercase; color: var(--muted); margin: 0 0 14px; font-weight: 500; }
h3 { font-size: 12px; letter-spacing: .09em; text-transform: uppercase; color: var(--muted); margin: 22px 0 10px; font-weight: 500; }
.muted { color: var(--muted); }
.error { color: #d03b3b; }
.scroll-x { overflow-x: auto; }

.topbar { display: flex; align-items: center; gap: 18px; flex-wrap: wrap; padding: 12px 20px; }
.brand { font-weight: 650; }
.pick { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--muted); }
select, input, button {
  font: inherit; color: var(--text);
  background: rgba(255, 255, 255, .05);
  border: 1px solid var(--stroke);
  border-radius: 8px; padding: 7px 10px;
}
button { cursor: pointer; transition: transform .18s var(--spring), background .18s var(--ease); }
button:hover { background: rgba(255, 255, 255, .10); transform: translateY(-1px); }
.ghost { margin-left: auto; }
:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }

.status { display: flex; align-items: center; gap: 7px; font-size: 12px; color: var(--muted); }
.dot { width: 8px; height: 8px; border-radius: 50%; background: #0ca30c; }
.status.stale .dot { background: #d03b3b; }

.gate { position: fixed; inset: 0; display: grid; place-items: center; }
.gate form { display: grid; gap: 10px; width: min(360px, 90vw); }

.split { display: grid; grid-template-columns: 1fr 1fr; gap: 18px; }
@media (max-width: 900px) { .split { grid-template-columns: 1fr; } }

table.grid { width: 100%; border-collapse: collapse; }
table.grid th { text-align: left; font-size: 11px; letter-spacing: .07em; text-transform: uppercase; color: var(--muted); padding: 0 12px 10px 0; font-weight: 500; }
table.grid td { padding: 10px 12px 10px 0; border-top: 1px solid var(--grid); font-variant-numeric: tabular-nums; }
tr.row { cursor: pointer; transition: background .18s var(--ease); }
tr.row:hover { background: rgba(255, 255, 255, .05); }
tr.row[aria-selected="true"] { background: rgba(57, 135, 229, .16); }
code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; color: var(--text-2); }

/* Stacked severity bar. The 2px gaps are the spec'd spacer between adjacent
   fills - never a border around each segment. */
.stack { display: flex; gap: 2px; height: 9px; width: 170px; border-radius: 5px; overflow: hidden; background: rgba(255, 255, 255, .06); }
.stack > span { display: block; transition: flex-grow .6s var(--ease); }

.pill { padding: 3px 9px; border-radius: 999px; font-size: 12px; font-weight: 600; display: inline-flex; align-items: center; gap: 6px; }
.pill .swatch { width: 8px; height: 8px; border-radius: 50%; }

.cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 18px; }
@media (max-width: 900px) { .cards { grid-template-columns: repeat(2, 1fr); } }
.card {
  background: var(--panel); border: 1px solid var(--stroke); border-radius: 14px;
  padding: 16px 18px; cursor: pointer; text-align: left; width: 100%;
  animation: rise .5s var(--ease) both; animation-delay: calc(var(--i, 0) * 60ms);
  transition: transform .2s var(--spring), border-color .2s var(--ease);
}
.card:hover { transform: translateY(-3px); border-color: rgba(255, 255, 255, .28); }
.card[aria-pressed="true"] { border-color: var(--accent); }
.card .label { display: flex; align-items: center; gap: 7px; font-size: 11px; letter-spacing: .09em; text-transform: uppercase; color: var(--muted); }
/* Proportional figures on display numbers: tabular-nums makes 121 look loose. */
.card .value { font-size: 34px; font-weight: 650; line-height: 1.15; }
.card .delta { font-size: 12px; }
```

- [ ] **Step 7: Write `src/components/StackedSeverityBar.tsx`**

```tsx
import { SEVERITIES, SEVERITY_COLOR } from '../theme'
import type { Counts } from '../types'

/**
 * Composition at a glance: two branches with ten findings each look very
 * different if one of them is all critical. Segments are ordered
 * critical -> low so the ramp reads light-to-dark left-to-right.
 */
export function StackedSeverityBar({ counts }: { counts: Counts }) {
  const total = SEVERITIES.reduce((sum, s) => sum + counts[s], 0)
  const label = total === 0
    ? 'no findings'
    : SEVERITIES.filter(s => counts[s] > 0).map(s => `${counts[s]} ${s}`).join(', ')
  return (
    <div className="stack" role="img" aria-label={label} title={label}>
      {SEVERITIES.filter(s => counts[s] > 0).map(s => (
        <span key={s} style={{ flexGrow: counts[s], background: SEVERITY_COLOR[s] }} />
      ))}
    </div>
  )
}
```

- [ ] **Step 8: Write `src/components/TopBar.tsx`**

```tsx
import type { Branch, Repo } from '../types'

interface Props {
  repos: Repo[]
  repoId: number
  branches: Branch[]
  branch: string
  status: string
  stale: boolean
  onRepo: (id: number) => void
  onBranch: (name: string) => void
  onSignOut: () => void
}

export function TopBar(props: Props) {
  return (
    <header className="topbar panel">
      <span className="brand">Codebase&nbsp;Analyser</span>
      <label className="pick">
        Repo
        <select value={props.repoId} onChange={e => props.onRepo(Number(e.target.value))}>
          {props.repos.map(r => <option key={r.id} value={r.id}>{r.remote_url}</option>)}
        </select>
      </label>
      <label className="pick">
        Branch
        <select value={props.branch} onChange={e => props.onBranch(e.target.value)}>
          {props.branches.map(b => <option key={b.name} value={b.name}>{b.name}</option>)}
        </select>
      </label>
      <span className={props.stale ? 'status stale' : 'status'}>
        <i className="dot" />{props.status}
      </span>
      <button className="ghost" onClick={props.onSignOut}>Sign out</button>
    </header>
  )
}
```

- [ ] **Step 9: Write `src/components/BranchTable.tsx`**

```tsx
import { StackedSeverityBar } from './StackedSeverityBar'
import { healthStatus, relativeTime } from '../theme'
import type { Branch, Counts } from '../types'

/**
 * Mirrors the server's weighting in api/metrics.go. The API scores only the
 * branch being viewed; this table summarises all of them, so the arithmetic
 * is repeated here. Keep the weights in sync.
 */
export function branchHealth(counts: Counts): number {
  const penalty = counts.critical * 12 + counts.high * 6 + counts.medium * 2 + counts.low
  return Math.max(0, Math.min(100, 100 - penalty))
}

export function BranchTable({ branches, selected, onSelect }: {
  branches: Branch[]
  selected: string
  onSelect: (name: string) => void
}) {
  return (
    <section className="panel" style={{ ['--i' as string]: 1 }}>
      <h2>All branches</h2>
      {branches.length === 0 ? <p className="muted">Nothing has been pushed for this repo yet.</p> : (
        <div className="scroll-x">
          <table className="grid">
            <thead>
              <tr><th>Branch</th><th>Last run</th><th>Commit</th><th>Composition</th><th>Health</th></tr>
            </thead>
            <tbody>
              {branches.map(b => {
                const health = branchHealth(b.counts)
                const status = healthStatus(health)
                return (
                  <tr key={b.name} className="row" aria-selected={b.name === selected}
                      onClick={() => onSelect(b.name)}>
                    <td>{b.name}</td>
                    <td className="muted">{relativeTime(b.last_run_at)}</td>
                    <td><code>{b.commit_sha.slice(0, 8)}</code></td>
                    <td><StackedSeverityBar counts={b.counts} /></td>
                    <td>
                      <span className="pill" style={{ background: 'rgba(255,255,255,.06)', color: status.color }}>
                        <i className="swatch" style={{ background: status.color }} />
                        {health} · {status.label}
                      </span>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
```

- [ ] **Step 10: Write `src/components/SeverityCards.tsx`**

```tsx
import { Line, LineChart, ResponsiveContainer } from 'recharts'
import { SEVERITIES, SEVERITY_COLOR, STATUS } from '../theme'
import type { CurrentRun, HistoryPoint, Severity } from '../types'

interface Props {
  current: CurrentRun | null
  history: HistoryPoint[]
  filter: Severity | null
  onFilter: (s: Severity | null) => void
}

/**
 * A KPI row of stat tiles - value, delta, sparkline - not a grouped bar
 * chart. Each tile is a button: clicking filters the findings table to that
 * severity, clicking again clears.
 */
export function SeverityCards({ current, history, filter, onFilter }: Props) {
  return (
    <section className="cards">
      {SEVERITIES.map((sev, i) => {
        const value = current ? current.run.counts[sev] : 0
        const change = current ? current.deltas[sev] : 0
        const series = history.map(p => ({ v: p.counts[sev] }))
        return (
          <button
            key={sev}
            className="card"
            style={{ ['--i' as string]: i + 1 }}
            aria-pressed={filter === sev}
            aria-label={`${value} ${sev} findings. ${describeDelta(change)}. Click to filter the findings table.`}
            onClick={() => onFilter(filter === sev ? null : sev)}
          >
            <span className="label">
              <i className="swatch" style={{ background: SEVERITY_COLOR[sev], width: 9, height: 9, borderRadius: 2 }} />
              {sev}
            </span>
            <div className="value">{value}</div>
            <div className="delta" style={{ color: deltaColor(change) }}>{describeDelta(change)}</div>
            <div style={{ height: 26, marginTop: 6 }}>
              {series.length > 1 && (
                <ResponsiveContainer width="100%" height={26}>
                  <LineChart data={series}>
                    <Line type="monotone" dataKey="v" stroke={SEVERITY_COLOR[sev]}
                          strokeWidth={2} dot={false} isAnimationActive={false} />
                  </LineChart>
                </ResponsiveContainer>
              )}
            </div>
          </button>
        )
      })}
    </section>
  )
}

function describeDelta(change: number): string {
  if (change === 0) return 'no change'
  return `${change > 0 ? '+' : ''}${change} vs previous run`
}

// More findings is bad, fewer is good - status hues on a lone labelled mark.
function deltaColor(change: number): string {
  if (change > 0) return STATUS.critical
  if (change < 0) return STATUS.good
  return '#898781'
}
```

- [ ] **Step 11: Write `src/App.tsx`**

```tsx
import { useCallback, useEffect, useState } from 'react'
import { ApiError, fetchJSON } from './api'
import { relativeTime } from './theme'
import { TopBar } from './components/TopBar'
import { BranchTable } from './components/BranchTable'
import { SeverityCards } from './components/SeverityCards'
import type { DashboardData, Repo, RunDetail, Severity } from './types'

export default function App() {
  const [token, setToken] = useState(() => localStorage.getItem('analyser_token') ?? '')
  const [gateError, setGateError] = useState('')
  const [repos, setRepos] = useState<Repo[]>([])
  const [repoId, setRepoId] = useState<number | null>(null)
  const [branch, setBranch] = useState('')
  const [data, setData] = useState<DashboardData | null>(null)
  const [viewing, setViewing] = useState<RunDetail | null>(null)
  const [filter, setFilter] = useState<Severity | null>(null)
  const [status, setStatus] = useState('connecting')
  const [stale, setStale] = useState(false)

  const signOut = useCallback((message = '') => {
    localStorage.removeItem('analyser_token')
    setToken('')
    setData(null)
    setGateError(message)
  }, [])

  // Repo list: once per token.
  useEffect(() => {
    if (!token) return
    let cancelled = false
    fetchJSON<Repo[]>('/api/admin/repos', token)
      .then(list => {
        if (cancelled) return
        setRepos(list)
        setRepoId(list.length ? list[0].id : null)
        if (!list.length) { setStatus('no repos registered'); setStale(true) }
      })
      .catch(err => {
        if (err instanceof ApiError && err.status === 401) signOut('That token was rejected.')
        else { setStatus('load failed'); setStale(true) }
      })
    return () => { cancelled = true }
  }, [token, signOut])

  // Dashboard payload: whenever the repo or branch changes.
  useEffect(() => {
    if (!token || repoId === null) return
    let cancelled = false
    const query = branch ? `?branch=${encodeURIComponent(branch)}` : ''
    fetchJSON<DashboardData>(`/api/repos/${repoId}/dashboard${query}`, token)
      .then(payload => {
        if (cancelled) return
        setData(payload)
        setBranch(payload.branch)
        setViewing(null)   // never leave an unrelated run in the findings table
        setStale(false)
        setStatus(payload.current ? `live · ${relativeTime(payload.current.run.pushed_at)}` : 'no runs')
      })
      .catch(err => {
        if (err instanceof ApiError && err.status === 401) signOut('That token was rejected.')
        else { setStatus('load failed'); setStale(true) }
      })
    return () => { cancelled = true }
  }, [token, repoId, branch, signOut])

  if (!token) {
    return (
      <>
        <div className="bg" aria-hidden />
        <div className="gate">
          <form
            className="panel"
            onSubmit={e => {
              e.preventDefault()
              const value = new FormData(e.currentTarget).get('token')
              const next = String(value ?? '').trim()
              if (!next) return
              localStorage.setItem('analyser_token', next)
              setGateError('')
              setToken(next)
            }}
          >
            <h1>Codebase Analyser</h1>
            <p className="muted">Enter the dashboard admin token.</p>
            <input name="token" type="password" autoComplete="current-password"
                   placeholder="admin token" aria-label="admin token" required />
            <button type="submit">Unlock</button>
            {gateError && <p className="error" role="alert">{gateError}</p>}
          </form>
        </div>
      </>
    )
  }

  return (
    <>
      <div className="bg" aria-hidden />
      <TopBar
        repos={repos}
        repoId={repoId ?? 0}
        branches={data?.branches ?? []}
        branch={branch}
        status={status}
        stale={stale}
        onRepo={id => { setRepoId(id); setBranch(''); setFilter(null) }}
        onBranch={name => { setBranch(name); setFilter(null) }}
        onSignOut={() => signOut()}
      />
      {data && (
        <>
          <BranchTable
            branches={data.branches}
            selected={branch}
            onSelect={name => { setBranch(name); setFilter(null) }}
          />
          <SeverityCards
            current={data.current}
            history={data.history}
            filter={filter}
            onFilter={setFilter}
          />
          {/* Task 8 inserts TrendChart, HealthTile, CategoryBars, SinceLastRun here. */}
          {/* Task 9 inserts ToolStatusPanel, TopFiles, ActivityFeed, FindingsTable here. */}
        </>
      )}
    </>
  )
}
```

The unused `viewing` state and its setter are wired in Task 9; leave them declared (TypeScript's `noUnusedLocals` covers locals, not state destructuring, so this compiles).

- [ ] **Step 12: Write the test fixtures and component tests**

`src/test/fixtures.ts`:

```ts
import type { Branch, CurrentRun, DashboardData, Finding, HistoryPoint } from '../types'

export function counts(critical = 0, high = 0, medium = 0, low = 0) {
  return { critical, high, medium, low }
}

export function finding(over: Partial<Finding> = {}): Finding {
  return {
    file: 'a.go', line: 4, tool: 'gosec', ruleID: 'G101',
    category: 'security', severity: 'high',
    message: 'hardcoded credential', explanation: 'leaks secrets in the binary',
    ...over,
  }
}

export function branch(over: Partial<Branch> = {}): Branch {
  return {
    name: 'main', last_run_at: new Date().toISOString(),
    commit_sha: 'abcdef1234567890', run_id: 1, counts: counts(1, 2, 0, 3),
    ...over,
  }
}

export function point(over: Partial<HistoryPoint> = {}): HistoryPoint {
  return {
    run_id: 1, commit_sha: 'abcdef1234567890', pushed_at: new Date().toISOString(),
    counts: counts(1, 2, 0, 3), health: 72, new: 1, fixed: 2,
    ...over,
  }
}

export function current(over: Partial<CurrentRun> = {}): CurrentRun {
  return {
    run: {
      id: 1, repo_id: 1, branch: 'main', commit_sha: 'abcdef1234567890',
      pushed_at: new Date().toISOString(), counts: counts(1, 2, 0, 3),
      tools: [{ name: 'gosec', skipped: false }, { name: 'clippy', skipped: true, error: 'install failed' }],
    },
    health: 72, deltas: counts(1, -1, 0, 0), new: 1, fixed: 2,
    categories: { correctness: 2, concurrency: 0, security: 4, operational: 0 },
    top_files: [{ file: 'a.go', count: 4 }, { file: 'b.go', count: 2 }],
    findings: [finding(), finding({ severity: 'critical', file: 'b.go', ruleID: 'G102', explanation: '' })],
    ...over,
  }
}

export function dashboard(over: Partial<DashboardData> = {}): DashboardData {
  return {
    repo: { id: 1, remote_url: 'github.com/acme/widgets', registered_at: new Date().toISOString() },
    branch: 'main',
    branches: [branch(), branch({ name: 'feature', counts: counts(0, 0, 1, 1) })],
    history: [point({ commit_sha: 'aaa1111111' }), point({ commit_sha: 'bbb2222222' })],
    current: current(),
    ...over,
  }
}
```

`src/components/__tests__/BranchTable.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BranchTable, branchHealth } from '../BranchTable'
import { branch, counts } from '../../test/fixtures'

test('selecting a row reports the branch to the parent', async () => {
  const onSelect = vi.fn()
  render(<BranchTable branches={[branch(), branch({ name: 'feature' })]} selected="main" onSelect={onSelect} />)

  await userEvent.click(screen.getByText('feature'))
  expect(onSelect).toHaveBeenCalledWith('feature')
})

test('the selected branch is marked, and severity composition is described in text', () => {
  render(<BranchTable branches={[branch({ counts: counts(1, 0, 0, 2) })]} selected="main" onSelect={() => {}} />)

  expect(screen.getByRole('row', { selected: true })).toHaveTextContent('main')
  // Composition must not be conveyed by color alone.
  expect(screen.getByRole('img', { name: '1 critical, 2 low' })).toBeInTheDocument()
})

test('branchHealth weights a critical above a low and clamps at zero', () => {
  expect(branchHealth(counts(0, 0, 0, 0))).toBe(100)
  expect(branchHealth(counts(1, 0, 0, 0))).toBeLessThan(branchHealth(counts(0, 0, 0, 1)))
  expect(branchHealth(counts(50, 50, 50, 50))).toBe(0)
})
```

`src/components/__tests__/SeverityCards.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SeverityCards } from '../SeverityCards'
import { current, point } from '../../test/fixtures'

test('clicking a card sets the filter and clicking the pressed card clears it', async () => {
  const onFilter = vi.fn()
  const { rerender } = render(
    <SeverityCards current={current()} history={[point()]} filter={null} onFilter={onFilter} />,
  )

  await userEvent.click(screen.getByRole('button', { name: /critical findings/i }))
  expect(onFilter).toHaveBeenCalledWith('critical')

  rerender(<SeverityCards current={current()} history={[point()]} filter="critical" onFilter={onFilter} />)
  await userEvent.click(screen.getByRole('button', { name: /critical findings/i }))
  expect(onFilter).toHaveBeenLastCalledWith(null)
})

test('each card states its count and its delta as text', () => {
  render(<SeverityCards current={current()} history={[point()]} filter={null} onFilter={() => {}} />)

  expect(screen.getByRole('button', { name: /1 critical findings/i })).toHaveTextContent('+1 vs previous run')
  expect(screen.getByRole('button', { name: /2 high findings/i })).toHaveTextContent('-1 vs previous run')
})

test('an empty run renders zeroes rather than crashing', () => {
  render(<SeverityCards current={null} history={[]} filter={null} onFilter={() => {}} />)
  expect(screen.getAllByText('0')).toHaveLength(4)
})
```

- [ ] **Step 13: Run the frontend tests and build**

```bash
cd internal/dashboard/web/ui
npm test
npm run build
```

Expected: all tests PASS, and `npm run build` writes `internal/dashboard/web/dist/index.html` plus hashed asset files.

- [ ] **Step 14: Replace `internal/dashboard/web/embed.go` and delete the placeholder**

```go
// Package web carries the dashboard's frontend, compiled into the binary so a
// deployment is one file plus a Postgres URL.
//
// dist/ is a committed build artifact: `go build` cannot run npm, so the
// output of `npm run build` (in ui/) is checked in. Rebuild it whenever
// anything under ui/src changes.
package web

import "embed"

// all: is required - Vite emits an assets/ directory whose files the default
// embed pattern would skip if any were named with a leading underscore.
//
//go:embed all:dist
var files embed.FS

// Assets is the frontend's root filesystem, served at /.
var Assets = mustSub()
```

Serving needs `dist` as the root rather than the parent directory, so add:

```go
import (
	"embed"
	"io/fs"
)

func mustSub() fs.FS {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic("embedded dist/ is missing - run `npm run build` in internal/dashboard/web/ui: " + err.Error())
	}
	return sub
}
```

Delete `internal/dashboard/web/index.html` (Task 5's placeholder).

- [ ] **Step 15: Replace `internal/dashboard/web/embed_test.go`**

```go
package web

import (
	"io/fs"
	"strings"
	"testing"
)

// Guards the embed wiring: dist/ is a committed build artifact, so the way
// this breaks is someone changing the UI and forgetting to rebuild - which
// ships a binary serving a stale or missing page.
func TestBuiltAssetsAreEmbedded(t *testing.T) {
	index, err := fs.ReadFile(Assets, "index.html")
	if err != nil {
		t.Fatalf("dist/index.html is not embedded (run `npm run build` in ui/): %v", err)
	}
	if !strings.Contains(string(index), "<script") {
		t.Error("dist/index.html has no script tag; it looks like a placeholder, not a Vite build")
	}
	entries, err := fs.ReadDir(Assets, "assets")
	if err != nil || len(entries) == 0 {
		t.Errorf("dist/assets is empty or missing: %v", err)
	}
}
```

- [ ] **Step 16: Serve the real bundle**

```bash
DATABASE_URL="$DASHBOARD_TEST_DSN" DASHBOARD_ADMIN_TOKEN=dev go run ./cmd/dashboard &
```

Open http://localhost:8080 and confirm, after pushing a couple of runs with the CLI:
- the gate accepts the admin token, remembers it across reload, and rejects a wrong one with a visible message;
- repo and branch selectors are populated and switching either reloads the data;
- the branches table lists every branch, most recent first, with proportional stacked bars and a health pill that names its state in text;
- the four severity tiles show value, delta and sparkline, and toggle their pressed state on click;
- panels enter staggered.

- [ ] **Step 17: Verify everything**

Run: `go test ./internal/dashboard/... -count=1 && go build ./... && go vet ./... && gofmt -l .`
Expected: PASS and no output.

- [ ] **Step 18: Hand back to the user**

Do NOT run git commands. Report the files created/modified — including that `internal/dashboard/web/dist/` must be committed — and stop so the user can commit.

---

### Task 8: Trend chart, health tile, category bars, since last run

**Files:**
- Create: `internal/dashboard/web/ui/src/components/{TrendChart,HealthTile,CategoryBars,SinceLastRun,Bars}.tsx`
- Create: `internal/dashboard/web/ui/src/components/__tests__/TrendChart.test.tsx`
- Modify: `internal/dashboard/web/ui/src/App.tsx` (mount the four views)
- Modify: `internal/dashboard/web/ui/src/styles.css` (append this task's rules)

**Interfaces:**
- Consumes: `state.data.history`, `state.data.current`, and everything from `theme.ts`.
- Produces: `Bars` (a single-series magnitude list Task 9 reuses for top files).

**Chart rules this task must not break** (they come from the dataviz method, not taste):
- Severity lines use `SEVERITY_COLOR` (the ordinal ramp), never the status hues.
- One y-axis. Never two.
- Gridlines and axes are **solid** hairlines — no dashing.
- A legend is always present for ≥2 series, and with four series the endpoint of each line is **directly labelled**; never a number on every point.
- The tooltip enhances but never gates: the panel carries a **table view** toggle showing the same numbers.
- Single-series bars are one color (`CHART.series1`), never a value-ramp across nominal categories.

- [ ] **Step 1: Append to `styles.css`**

```css
.chart-head { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; }
.chart-head button { font-size: 12px; padding: 4px 10px; }

.legend { display: flex; gap: 10px; flex-wrap: wrap; margin-top: 10px; }
.legend button { display: flex; align-items: center; gap: 6px; font-size: 12px; padding: 4px 9px; }
.legend button[aria-pressed="false"] { opacity: .4; }
.legend i { width: 9px; height: 9px; border-radius: 2px; }

.tooltip { background: var(--surface); border: 1px solid var(--stroke); border-radius: 10px; padding: 10px 12px; font-size: 13px; }
.tooltip .when { color: var(--muted); display: block; margin-bottom: 6px; }
.tooltip .line { display: flex; align-items: center; gap: 7px; }
.tooltip .line b { font-variant-numeric: tabular-nums; min-width: 2ch; text-align: right; }

/* Hero figure: proportional digits, system sans, no gauge. A single number
   is a stat tile, not a one-bar chart. */
.hero { display: grid; gap: 6px; justify-items: start; }
.hero .figure { font-size: 56px; font-weight: 650; line-height: 1; }
.hero .sub { color: var(--muted); font-size: 13px; }

.bars { display: grid; gap: 10px; }
.bars .bar { display: grid; grid-template-columns: minmax(90px, 34%) 1fr auto; align-items: center; gap: 10px; font-size: 13px; }
.bars .track { height: 10px; border-radius: 5px; background: rgba(255, 255, 255, .06); }
/* 4px rounded data-end, anchored to the baseline. */
.bars .fill { height: 100%; width: 0; border-radius: 0 4px 4px 0; transition: width .8s var(--ease); }
.bars .n { font-variant-numeric: tabular-nums; color: var(--text-2); min-width: 3ch; text-align: right; }
.bars .name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.since { display: flex; gap: 30px; align-items: baseline; flex-wrap: wrap; }
.since .n { font-size: 40px; font-weight: 650; line-height: 1.1; }
```

- [ ] **Step 2: Write `src/components/Bars.tsx`**

```tsx
import { CHART } from '../theme'

export interface BarDatum { name: string; value: number; title?: string }

/**
 * A single-series magnitude list. One color for every bar: coloring each bar
 * darker-where-bigger would double-encode the length that is already the
 * whole point, and burns the only free channel.
 *
 * Deliberately CSS rather than Recharts - the labels are long file paths and
 * category names, and a fixed-height chart container is exactly how axis
 * bands get clipped.
 */
export function Bars({ data }: { data: BarDatum[] }) {
  const max = Math.max(1, ...data.map(d => d.value))
  return (
    <div className="bars">
      {data.map(d => (
        <div className="bar" key={d.name}>
          <span className="name" title={d.title ?? d.name}>{d.name}</span>
          <div className="track">
            <div className="fill" style={{ width: `${(d.value / max) * 100}%`, background: CHART.series1 }} />
          </div>
          <span className="n">{d.value}</span>
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 3: Write `src/components/TrendChart.tsx`**

```tsx
import { useState } from 'react'
import {
  CartesianGrid, Line, LineChart, ReferenceDot, ResponsiveContainer, Tooltip, XAxis, YAxis,
} from 'recharts'
import { CHART, SEVERITIES, SEVERITY_COLOR, relativeTime } from '../theme'
import type { HistoryPoint, Severity } from '../types'

interface Row {
  label: string
  commit: string
  when: string
  critical: number
  high: number
  medium: number
  low: number
}

export function TrendChart({ history }: { history: HistoryPoint[] }) {
  const [hidden, setHidden] = useState<Set<Severity>>(new Set())
  const [asTable, setAsTable] = useState(false)

  const shown = SEVERITIES.filter(s => !hidden.has(s))
  const rows: Row[] = history.map(p => ({
    label: p.commit_sha.slice(0, 7),
    commit: p.commit_sha,
    when: p.pushed_at,
    ...p.counts,
  }))

  const toggle = (sev: Severity) => {
    const next = new Set(hidden)
    if (next.has(sev)) next.delete(sev)
    else next.add(sev)
    // Never let the reader blank the chart entirely.
    if (next.size === SEVERITIES.length) next.clear()
    setHidden(next)
  }

  // The busiest run, called out on the chart so nobody has to scan for it.
  let peakIndex = 0
  let peakValue = 0
  rows.forEach((row, i) => {
    const worst = Math.max(...shown.map(s => row[s]))
    if (worst > peakValue) { peakValue = worst; peakIndex = i }
  })
  const peakSeverity = shown.find(s => rows[peakIndex]?.[s] === peakValue)

  return (
    <section className="panel" style={{ ['--i' as string]: 3 }}>
      <div className="chart-head">
        <h2>Findings over time</h2>
        <button onClick={() => setAsTable(v => !v)} aria-pressed={asTable}>
          {asTable ? 'Chart' : 'Table'}
        </button>
      </div>

      {history.length < 2 ? (
        <p className="muted">
          {history.length ? 'One run so far — the trend appears from the second run.' : 'No runs on this branch yet.'}
        </p>
      ) : asTable ? (
        <div className="scroll-x">
          <table className="grid">
            <thead>
              <tr><th>Commit</th><th>When</th>{SEVERITIES.map(s => <th key={s}>{s}</th>)}</tr>
            </thead>
            <tbody>
              {rows.map(row => (
                <tr key={row.commit}>
                  <td><code>{row.label}</code></td>
                  <td className="muted">{relativeTime(row.when)}</td>
                  {SEVERITIES.map(s => <td key={s}>{row[s]}</td>)}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <>
          <ResponsiveContainer width="100%" height={260}>
            <LineChart data={rows} margin={{ top: 12, right: 48, bottom: 4, left: 0 }}>
              {/* Solid hairlines. Dashed gridlines read as thresholds. */}
              <CartesianGrid stroke={CHART.grid} vertical={false} />
              <XAxis dataKey="label" stroke={CHART.axis} tick={{ fill: CHART.muted, fontSize: 11 }} tickLine={false} />
              <YAxis allowDecimals={false} stroke={CHART.axis} tick={{ fill: CHART.muted, fontSize: 11 }}
                     tickLine={false} width={34} />
              <Tooltip
                cursor={{ stroke: CHART.series1, strokeWidth: 1 }}
                content={({ active, payload, label }) =>
                  active && payload?.length ? (
                    <div className="tooltip">
                      <span className="when">
                        {String(label)} · {relativeTime(payload[0].payload.when)}
                      </span>
                      {shown.map(s => (
                        <span className="line" key={s}>
                          <i style={{ width: 9, height: 9, borderRadius: 2, background: SEVERITY_COLOR[s] }} />
                          <b>{payload[0].payload[s]}</b> {s}
                        </span>
                      ))}
                    </div>
                  ) : null
                }
              />
              {shown.map(sev => (
                <Line
                  key={sev}
                  type="monotone"
                  dataKey={sev}
                  name={sev}
                  stroke={SEVERITY_COLOR[sev]}
                  strokeWidth={2}
                  dot={false}
                  activeDot={{ r: 5, strokeWidth: 2, stroke: CHART.surface }}
                  isAnimationActive
                  animationDuration={900}
                  // Selective direct label: the endpoint only, never every point.
                  label={(props: { index?: number; x?: number; y?: number; value?: number }) =>
                    props.index === rows.length - 1 ? (
                      <text x={(props.x ?? 0) + 8} y={(props.y ?? 0) + 4}
                            fill={SEVERITY_COLOR[sev]} fontSize={11}>{sev}</text>
                    ) : <g />
                  }
                />
              ))}
              {peakValue > 0 && peakSeverity && (
                <ReferenceDot
                  x={rows[peakIndex].label}
                  y={peakValue}
                  r={4}
                  fill={SEVERITY_COLOR[peakSeverity]}
                  stroke={CHART.surface}
                  strokeWidth={2}
                  label={{ value: 'busiest', position: 'top', fill: CHART.muted, fontSize: 10 }}
                />
              )}
            </LineChart>
          </ResponsiveContainer>

          <div className="legend">
            {SEVERITIES.map(sev => (
              <button key={sev} aria-pressed={!hidden.has(sev)} onClick={() => toggle(sev)}>
                <i style={{ background: SEVERITY_COLOR[sev] }} />
                {sev}
              </button>
            ))}
          </div>
        </>
      )}
    </section>
  )
}
```

- [ ] **Step 4: Write `src/components/HealthTile.tsx`**

```tsx
import { Line, LineChart, ResponsiveContainer } from 'recharts'
import { CHART, healthStatus } from '../theme'
import type { HistoryPoint } from '../types'

/**
 * One number is a stat tile, not a gauge: a radial gauge is a one-bar bar
 * chart with extra geometry. Hero figure in the system sans with proportional
 * digits, its status color paired with a text label so the state never rides
 * on color alone.
 */
export function HealthTile({ health, history }: { health: number | null; history: HistoryPoint[] }) {
  const status = health === null ? null : healthStatus(health)
  return (
    <section className="panel" style={{ ['--i' as string]: 4 }}>
      <h2>Health score</h2>
      {health === null || !status ? (
        <p className="muted">No runs on this branch yet.</p>
      ) : (
        <div className="hero">
          <span className="figure" style={{ color: status.color }}>{health}</span>
          <span className="pill" style={{ background: 'rgba(255,255,255,.06)', color: status.color }}>
            <i className="swatch" style={{ background: status.color }} />
            {status.label}
          </span>
          <span className="sub">severity-weighted out of 100, adjusted for the trend</span>
          {history.length > 1 && (
            <div style={{ width: '100%', height: 44 }}>
              <ResponsiveContainer width="100%" height={44}>
                <LineChart data={history.map(p => ({ v: p.health }))}>
                  <Line type="monotone" dataKey="v" stroke={CHART.series1} strokeWidth={2}
                        dot={false} isAnimationActive={false} />
                </LineChart>
              </ResponsiveContainer>
            </div>
          )}
        </div>
      )}
    </section>
  )
}
```

- [ ] **Step 5: Write `src/components/CategoryBars.tsx`**

```tsx
import { Bars } from './Bars'

/**
 * Where the risk concentrates - information the severity tiles structurally
 * cannot show. Four fixed nominal categories, so: one series, one color.
 */
export function CategoryBars({ categories }: { categories: Record<string, number> | null }) {
  return (
    <section className="panel" style={{ ['--i' as string]: 5 }}>
      <h2>Findings by category</h2>
      {!categories ? (
        <p className="muted">No runs on this branch yet.</p>
      ) : (
        <Bars data={Object.entries(categories).map(([name, value]) => ({ name, value }))} />
      )}
    </section>
  )
}
```

- [ ] **Step 6: Write `src/components/SinceLastRun.tsx`**

```tsx
import { STATUS } from '../theme'
import type { CurrentRun, HistoryPoint } from '../types'

/**
 * The most directly motivating number for someone checking in day to day.
 * Status hues on two lone labelled figures - more findings is bad, fewer is
 * good - which is exactly what status color is for.
 */
export function SinceLastRun({ current, history }: { current: CurrentRun | null; history: HistoryPoint[] }) {
  return (
    <section className="panel" style={{ ['--i' as string]: 6 }}>
      <h2>Since last run</h2>
      {!current ? (
        <p className="muted">No runs on this branch yet.</p>
      ) : (
        <div className="since">
          <div>
            <div className="n" style={{ color: STATUS.critical }}>{current.new}</div>
            <div className="muted">new</div>
          </div>
          <div>
            <div className="n" style={{ color: STATUS.good }}>{current.fixed}</div>
            <div className="muted">fixed</div>
          </div>
          <div className="muted">
            {history.length > 1
              ? `compared with ${history[history.length - 2].commit_sha.slice(0, 8)}`
              : 'first run on this branch — everything counts as new'}
          </div>
        </div>
      )}
    </section>
  )
}
```

- [ ] **Step 7: Mount the four views in `App.tsx`**

Add the imports and replace the Task 8 comment placeholder:

```tsx
import { TrendChart } from './components/TrendChart'
import { HealthTile } from './components/HealthTile'
import { CategoryBars } from './components/CategoryBars'
import { SinceLastRun } from './components/SinceLastRun'
```

```tsx
          <div className="split">
            <TrendChart history={data.history} />
            <HealthTile health={data.current?.health ?? null} history={data.history} />
          </div>
          <div className="split">
            <CategoryBars categories={data.current?.categories ?? null} />
            <SinceLastRun current={data.current} history={data.history} />
          </div>
```

- [ ] **Step 8: Write `src/components/__tests__/TrendChart.test.tsx`**

Recharts needs a sized container in jsdom; `ResponsiveContainer` reports 0×0 there, so these tests assert the interactive behaviour and the table view rather than SVG geometry.

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TrendChart } from '../TrendChart'
import { counts, point } from '../../test/fixtures'

const history = [
  point({ commit_sha: 'aaaaaaa111', counts: counts(1, 2, 3, 4) }),
  point({ commit_sha: 'bbbbbbb222', counts: counts(0, 5, 1, 2) }),
]

test('a legend entry toggles its series off and back on', async () => {
  render(<TrendChart history={history} />)
  const critical = screen.getByRole('button', { name: 'critical' })

  expect(critical).toHaveAttribute('aria-pressed', 'true')
  await userEvent.click(critical)
  expect(critical).toHaveAttribute('aria-pressed', 'false')
  await userEvent.click(critical)
  expect(critical).toHaveAttribute('aria-pressed', 'true')
})

test('hiding every series restores them all rather than blanking the chart', async () => {
  render(<TrendChart history={history} />)
  for (const name of ['critical', 'high', 'medium', 'low']) {
    await userEvent.click(screen.getByRole('button', { name }))
  }
  for (const name of ['critical', 'high', 'medium', 'low']) {
    expect(screen.getByRole('button', { name })).toHaveAttribute('aria-pressed', 'true')
  }
})

test('the table view exposes every value the chart encodes', async () => {
  render(<TrendChart history={history} />)
  await userEvent.click(screen.getByRole('button', { name: 'Table' }))

  const rows = screen.getAllByRole('row')
  expect(rows).toHaveLength(3) // header + two runs
  expect(rows[1]).toHaveTextContent('aaaaaaa')
  expect(rows[2]).toHaveTextContent('bbbbbbb')
})

test('a single run explains itself instead of drawing a one-point line', () => {
  render(<TrendChart history={[history[0]]} />)
  expect(screen.getByText(/trend appears from the second run/i)).toBeInTheDocument()
})
```

- [ ] **Step 9: Run the tests and build**

```bash
cd internal/dashboard/web/ui && npm test && npm run build
```

Expected: PASS, and a fresh `dist/`.

- [ ] **Step 10: Look at the page against real multi-run data**

Push at least three runs on one branch with genuinely different findings, then reload and confirm:
- the four severity lines animate in, use the blue ordinal ramp (not red/orange/yellow/green), and each is labelled at its right-hand endpoint;
- hovering shows one tooltip with the commit, relative time, and every visible series' exact count;
- the legend toggles series and the y-axis rescales;
- the busiest point is marked;
- the Table button shows the same numbers as a table;
- gridlines are solid hairlines, not dashed;
- the health tile is a hero number with a status label, and its sparkline tracks health;
- category and since-last-run panels render, and all four degrade to a "no runs" line on an empty branch.

- [ ] **Step 11: Verify everything**

Run: `go test ./internal/dashboard/... -count=1 && go build ./... && go vet ./... && gofmt -l .`
Expected: PASS and no output.

- [ ] **Step 12: Hand back to the user**

Do NOT run git commands. Report the files created/modified (including the rebuilt `dist/`) and stop so the user can commit.

---

### Task 9: Per-run new/fixed, tool status, pipeline diagram, top files, activity feed, findings table

The activity feed needs a real new/fixed delta per historical run, which the Task 5 payload does not carry — so this task extends the store and the payload first, then finishes the page.

**Files:**
- Modify: `internal/dashboard/store/runs.go` (add `FindingsForRuns`)
- Test: `internal/dashboard/store/runs_test.go` (append)
- Modify: `internal/dashboard/api/dashboard.go` (history points gain `new`/`fixed`)
- Test: `internal/dashboard/api/dashboard_test.go` (append)
- Create: `internal/dashboard/web/ui/src/components/{ToolStatusPanel,PipelineDiagram,TopFiles,ActivityFeed,FindingsTable}.tsx`
- Create: `internal/dashboard/web/ui/src/components/__tests__/{FindingsTable,ToolStatusPanel}.test.tsx`
- Modify: `internal/dashboard/web/ui/src/App.tsx` (mount them, wire run drill-down)
- Modify: `internal/dashboard/web/ui/src/styles.css` (append)

**Interfaces:**
- Produces: `(*Store).FindingsForRuns(ctx context.Context, runIDs []int64) (map[int64][]Finding, error)`; `historyPoint` gains `"new"` and `"fixed"` int fields (add them to `HistoryPoint` in `types.ts` — they are already declared there).

- [ ] **Step 1: Write the failing store test**

Append to `internal/dashboard/store/runs_test.go`:

```go
func TestFindingsForRunsGroupsByRun(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	repo, _, _ := s.CreateRepo(ctx, "github.com/acme/widgets")

	r1, _ := s.SaveRun(ctx, repo.ID, "main", "c1", nil, fixtureFindings(2))
	r2, _ := s.SaveRun(ctx, repo.ID, "main", "c2", nil, fixtureFindings(3))
	empty, _ := s.SaveRun(ctx, repo.ID, "main", "c3", nil, nil)

	got, err := s.FindingsForRuns(ctx, []int64{r1, r2, empty})
	if err != nil {
		t.Fatalf("FindingsForRuns: %v", err)
	}
	if len(got[r1]) != 2 || len(got[r2]) != 3 {
		t.Errorf("grouping = %d and %d findings, want 2 and 3", len(got[r1]), len(got[r2]))
	}
	if _, present := got[empty]; present {
		t.Error("a run with no findings must be absent from the map, not an empty entry")
	}
	if got[r1][0].Tool != "gosec" {
		t.Errorf("finding not fully scanned: %+v", got[r1][0])
	}

	none, err := s.FindingsForRuns(ctx, nil)
	if err != nil || len(none) != 0 {
		t.Errorf("FindingsForRuns(nil) = %v, %v; want an empty map and no error", none, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/dashboard/store/ -run TestFindingsForRuns -v`
Expected: FAIL to build — `undefined: FindingsForRuns`.

- [ ] **Step 3: Add `FindingsForRuns` to `internal/dashboard/store/runs.go`**

```go
// FindingsForRuns fetches several runs' findings in one query, grouped by run.
// The activity feed needs a new-vs-fixed delta for every run in the history,
// which would otherwise be one query per run.
func (s *Store) FindingsForRuns(ctx context.Context, runIDs []int64) (map[int64][]Finding, error) {
	out := map[int64][]Finding{}
	if len(runIDs) == 0 {
		return out, nil
	}
	// The IN list is built from int64s we control - no user input reaches this
	// string, and database/sql has no portable []int64 binding.
	ids := make([]string, len(runIDs))
	for i, id := range runIDs {
		ids[i] = strconv.FormatInt(id, 10)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT run_id, file, line, tool, rule_id, category, severity, message, explanation
		 FROM findings WHERE run_id IN (`+strings.Join(ids, ",")+`)
		 ORDER BY run_id, file, line, rule_id, id`)
	if err != nil {
		return nil, fmt.Errorf("list findings for runs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			runID int64
			f     Finding
		)
		if err := rows.Scan(&runID, &f.File, &f.Line, &f.Tool, &f.RuleID, &f.Category,
			&f.Severity, &f.Message, &f.Explanation); err != nil {
			return nil, fmt.Errorf("scan finding: %w", err)
		}
		out[runID] = append(out[runID], f)
	}
	return out, rows.Err()
}
```

Add `"strconv"` and `"strings"` to the file's imports.

- [ ] **Step 4: Run the store tests**

Run: `go test ./internal/dashboard/store/ -v -count=1`
Expected: PASS, 11 tests.

- [ ] **Step 5: Write the failing API test**

Append to `internal/dashboard/api/dashboard_test.go`:

```go
func TestHistoryCarriesNewAndFixedPerRun(t *testing.T) {
	srv, _ := newTestServer(t)
	repoID, token := registerRepo(t, srv, "github.com/acme/widgets")

	pushRun(t, srv, token, "main", "c1", []store.Finding{
		sev("high", "a.go", 1, "G101"),
		sev("high", "b.go", 2, "G102"),
	}, nil)
	pushRun(t, srv, token, "main", "c2", []store.Finding{
		sev("high", "a.go", 1, "G101"), // carried over
		sev("low", "c.go", 3, "G104"),  // new
	}, nil)

	got := getDashboard(t, srv, repoID, "")
	if len(got.History) != 2 {
		t.Fatalf("history = %d points, want 2", len(got.History))
	}
	if got.History[0].New != 2 || got.History[0].Fixed != 0 {
		t.Errorf("first run: new=%d fixed=%d, want everything new", got.History[0].New, got.History[0].Fixed)
	}
	if got.History[1].New != 1 || got.History[1].Fixed != 1 {
		t.Errorf("second run: new=%d fixed=%d, want new=1 (c.go) fixed=1 (b.go)", got.History[1].New, got.History[1].Fixed)
	}
	// The current-run summary must agree with its own history point.
	last := got.History[len(got.History)-1]
	if got.Current.New != last.New || got.Current.Fixed != last.Fixed {
		t.Errorf("current (new=%d fixed=%d) disagrees with its history point (new=%d fixed=%d)",
			got.Current.New, got.Current.Fixed, last.New, last.Fixed)
	}
}
```

- [ ] **Step 6: Add `new`/`fixed` to the history payload**

In `internal/dashboard/api/dashboard.go`, extend the struct:

```go
type historyPoint struct {
	RunID     int64          `json:"run_id"`
	CommitSHA string         `json:"commit_sha"`
	PushedAt  string         `json:"pushed_at"`
	Counts    map[string]int `json:"counts"`
	Health    int            `json:"health"`
	New       int            `json:"new"`
	Fixed     int            `json:"fixed"`
}
```

Replace the history-building loop and the "since last run" block in `dashboard` with a single pass over one bulk fetch:

```go
	runIDs := make([]int64, len(runs))
	for i, run := range runs {
		runIDs[i] = run.ID
	}
	findingsByRun, err := s.st.FindingsForRuns(ctx, runIDs)
	if err != nil {
		serverError(w, "list findings", err)
		return
	}

	for i, run := range runs {
		var prevCounts map[string]int
		var prevFindings []store.Finding
		if i > 0 {
			prevCounts = runs[i-1].Counts
			prevFindings = findingsByRun[runs[i-1].ID]
		}
		added, fixed := Diff(prevFindings, findingsByRun[run.ID])
		resp.History = append(resp.History, historyPoint{
			RunID:     run.ID,
			CommitSHA: run.CommitSHA,
			PushedAt:  run.PushedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Counts:    run.Counts,
			Health:    HealthScore(run.Counts, prevCounts),
			New:       added,
			Fixed:     fixed,
		})
	}

	current := runs[len(runs)-1]
	findings := findingsByRun[current.ID]
	if findings == nil {
		findings = []store.Finding{}
	}
	last := resp.History[len(resp.History)-1]
	var currentPrevCounts map[string]int
	if len(runs) > 1 {
		currentPrevCounts = runs[len(runs)-2].Counts
	}

	resp.Current = &currentRun{
		Run:        current,
		Health:     HealthScore(current.Counts, currentPrevCounts),
		Deltas:     deltas(current.Counts, currentPrevCounts),
		New:        last.New,
		Fixed:      last.Fixed,
		Categories: CategoryCounts(findings),
		TopFiles:   TopFiles(findings, topFileCount),
		Findings:   findings,
	}
	writeJSON(w, http.StatusOK, resp)
```

The old `FindingsForRun` call for the current run and the separate previous-run fetch both go away — one query now serves both views, and `current.new/fixed` is by construction the same number as its history point rather than a second computation that could drift.

- [ ] **Step 7: Run the API tests**

Run: `go test ./internal/dashboard/... -v -count=1`
Expected: PASS. `TestDashboardDefaultsToMainAndAssemblesEveryView` must still pass unchanged — its new=1/fixed=2 assertion is the regression guard for this refactor.

- [ ] **Step 8: Append to `styles.css`**

```css
.tools { display: grid; gap: 8px; }
.tool { display: flex; align-items: center; gap: 10px; font-size: 13px; }
.tool .state { width: 9px; height: 9px; border-radius: 50%; }
.tool.skipped .name { color: var(--muted); text-decoration: line-through; }

.pipe .node { fill: rgba(255, 255, 255, .05); stroke: var(--stroke); }
.pipe .node.off { fill: none; stroke-dasharray: 4 3; }
.pipe .label { fill: var(--text); font-size: 10px; }
.pipe .label.off { fill: var(--muted); }
.pipe .flow { stroke: var(--accent); stroke-width: 1.4; fill: none; opacity: .75; }
.pipe .flow.off { stroke: var(--muted); stroke-dasharray: 4 3; opacity: .35; }

.feed { display: grid; }
.feed .item { display: grid; grid-template-columns: 1fr auto auto; gap: 12px; align-items: center;
  padding: 9px 0; border-top: 1px solid var(--grid); cursor: pointer; text-align: left;
  background: none; border-left: 0; border-right: 0; border-bottom: 0; border-radius: 0; width: 100%; }
.feed .item:hover { background: rgba(255, 255, 255, .05); transform: none; }
.feed .delta { font-size: 12px; font-variant-numeric: tabular-nums; }

.finding { border-top: 1px solid var(--grid); }
.finding .head { display: grid; grid-template-columns: 84px minmax(140px, 220px) 1fr auto; gap: 12px;
  align-items: baseline; padding: 10px 0; cursor: pointer; text-align: left; width: 100%;
  background: none; border: 0; border-radius: 0; }
.finding .head:hover { background: rgba(255, 255, 255, .05); transform: none; }
.finding .sev { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; }
.finding .sev i { width: 9px; height: 9px; border-radius: 2px; }
.finding .msg { overflow-wrap: anywhere; }
.finding .chev { color: var(--muted); transition: transform .25s var(--spring); }
.finding .head[aria-expanded="true"] .chev { transform: rotate(90deg); }
/* Accordion via a grid-row transition: no measuring, no max-height guessing. */
.finding .detail { display: grid; grid-template-rows: 0fr; transition: grid-template-rows .3s var(--ease); }
.finding .detail.open { grid-template-rows: 1fr; }
.finding .detail > div { overflow: hidden; }
.finding .body { padding: 0 0 14px 96px; color: var(--text-2); font-size: 13px; max-width: 74ch; }
.finding .body .meta { display: block; color: var(--muted); padding-bottom: 8px; }
.empty { color: var(--muted); padding: 12px 0; }
```

- [ ] **Step 9: Write `src/components/PipelineDiagram.tsx`**

```tsx
import type { ToolStatus } from '../types'

// The five tools the CLI wraps. A tool absent from a run's statuses simply
// did not apply to this repo (no Rust source, no clippy).
const PIPELINE_TOOLS = ['golangci-lint', 'gosec', 'govulncheck', 'clippy', 'cargo-audit']

/**
 * The CLI's real pipeline. A skipped tool is drawn disconnected - dashed box,
 * dashed inbound edge, no outbound edge - so the diagram shows a broken wire
 * rather than quietly omitting the stage.
 */
export function PipelineDiagram({ tools }: { tools: ToolStatus[] }) {
  const byName = new Map(tools.map(t => [t.name, t]))
  const active = PIPELINE_TOOLS.filter(name => byName.has(name))
  if (!active.length) return null

  const rowH = 30
  const width = 620
  const height = Math.max(120, active.length * rowH + 30)
  const midY = height / 2

  const box = (x: number, y: number, label: string, off: boolean, w = 96) => (
    <g key={`${label}-${y}`}>
      <rect className={off ? 'node off' : 'node'} x={x} y={y - 12} width={w} height={24} rx={6} />
      <text className={off ? 'label off' : 'label'} x={x + w / 2} y={y + 4} textAnchor="middle">{label}</text>
    </g>
  )
  const edge = (fromX: number, fromY: number, toX: number, toY: number, off: boolean, key: string) => (
    <path key={key} className={off ? 'flow off' : 'flow'}
          d={`M${fromX} ${fromY} C${fromX + 26} ${fromY}, ${toX - 26} ${toY}, ${toX} ${toY}`} />
  )

  return (
    <div className="scroll-x">
      <svg className="pipe" viewBox={`0 0 ${width} ${height}`} width="100%" role="img"
           aria-label={`Analysis pipeline. ${active.map(n => `${n} ${byName.get(n)!.skipped ? 'skipped' : 'ran'}`).join(', ')}.`}>
        {box(6, midY, 'repository', false, 82)}
        {box(340, midY, 'normalize', false)}
        {box(452, midY, 'LLM explain', false)}
        {box(556, midY, 'report', false, 58)}
        {active.map((name, i) => {
          const y = midY - ((active.length - 1) * rowH) / 2 + i * rowH
          const skipped = byName.get(name)!.skipped
          return (
            <g key={name}>
              {edge(88, midY, 150, y, skipped, `in-${name}`)}
              {box(150, y, name, skipped, 150)}
              {!skipped && edge(300, y, 340, midY, false, `out-${name}`)}
            </g>
          )
        })}
        {edge(436, midY, 452, midY, false, 'normalize-explain')}
        {edge(548, midY, 556, midY, false, 'explain-report')}
      </svg>
    </div>
  )
}
```

- [ ] **Step 10: Write `src/components/ToolStatusPanel.tsx`**

```tsx
import { PipelineDiagram } from './PipelineDiagram'
import { STATUS } from '../theme'
import type { ToolStatus } from '../types'

/**
 * Makes the CLI's skip-and-continue behaviour visible. A tool that failed to
 * install contributes zero findings, and without this view that silence looks
 * like good news.
 */
export function ToolStatusPanel({ tools }: { tools: ToolStatus[] | null }) {
  return (
    <section className="panel" style={{ ['--i' as string]: 7 }}>
      <h2>Tool run status</h2>
      {!tools ? (
        <p className="muted">No runs on this branch yet.</p>
      ) : tools.length === 0 ? (
        <p className="muted">This run reported no tool statuses.</p>
      ) : (
        <>
          <div className="tools">
            {tools.map(tool => (
              <div className={tool.skipped ? 'tool skipped' : 'tool'} key={tool.name}>
                <i className="state" style={{ background: tool.skipped ? STATUS.critical : STATUS.good }} />
                <span className="name">{tool.name}</span>
                <span style={{ color: tool.skipped ? STATUS.critical : undefined }} className={tool.skipped ? '' : 'muted'}>
                  {tool.skipped ? `skipped — ${tool.error || 'no reason reported'}` : 'ran'}
                </span>
              </div>
            ))}
          </div>
          <h3>Analysis pipeline</h3>
          <PipelineDiagram tools={tools} />
        </>
      )}
    </section>
  )
}
```

- [ ] **Step 11: Write `src/components/TopFiles.tsx`**

```tsx
import { Bars } from './Bars'
import type { FileCount } from '../types'

export function TopFiles({ files }: { files: FileCount[] | null }) {
  return (
    <section className="panel" style={{ ['--i' as string]: 8 }}>
      <h2>Top offending files</h2>
      {!files ? (
        <p className="muted">No runs on this branch yet.</p>
      ) : files.length === 0 ? (
        <p className="empty">No findings in this run.</p>
      ) : (
        <Bars data={files.map(f => ({ name: shorten(f.file), value: f.count, title: f.file }))} />
      )}
    </section>
  )
}

// Long paths get their tail kept - the filename is what identifies it.
function shorten(path: string): string {
  return path.length <= 34 ? path : '…' + path.slice(-33)
}
```

- [ ] **Step 12: Write `src/components/ActivityFeed.tsx`**

```tsx
import { STATUS, healthStatus, relativeTime } from '../theme'
import type { HistoryPoint } from '../types'

/**
 * History without axes: scan the last several runs, see which way each one
 * went, click one to inspect it.
 */
export function ActivityFeed({ history, onSelectRun }: {
  history: HistoryPoint[]
  onSelectRun: (runID: number) => void
}) {
  const recent = history.slice(-8).reverse()
  return (
    <section className="panel" style={{ ['--i' as string]: 9 }}>
      <h2>Recent activity</h2>
      {recent.length === 0 ? (
        <p className="empty">No runs on this branch yet.</p>
      ) : (
        <div className="feed">
          {recent.map(point => {
            const status = healthStatus(point.health)
            return (
              <button className="item" key={point.run_id} onClick={() => onSelectRun(point.run_id)}
                      aria-label={`Run ${point.commit_sha.slice(0, 8)}: ${point.new} new, ${point.fixed} fixed, health ${point.health}`}>
                <span>
                  <code>{point.commit_sha.slice(0, 8)}</code>{' '}
                  <span className="muted">{relativeTime(point.pushed_at)}</span>
                </span>
                <span className="delta">
                  <span style={{ color: STATUS.critical }}>+{point.new}</span>
                  {' / '}
                  <span style={{ color: STATUS.good }}>−{point.fixed}</span>
                </span>
                <span className="pill" style={{ background: 'rgba(255,255,255,.06)', color: status.color }}>
                  <i className="swatch" style={{ background: status.color }} />
                  {point.health}
                </span>
              </button>
            )
          })}
        </div>
      )}
    </section>
  )
}
```

- [ ] **Step 13: Write `src/components/FindingsTable.tsx`**

```tsx
import { useState } from 'react'
import { SEVERITY_COLOR } from '../theme'
import type { Finding, Run, Severity } from '../types'

interface Props {
  run: Run | null
  findings: Finding[]
  filter: Severity | null
  viewingOlder: boolean
}

/**
 * The CLI's human-readable report, made browsable. Rows expand inline to the
 * LLM explanation rather than replacing the page, so "what's true now" and
 * "how did we get here" stay visible together.
 */
export function FindingsTable({ run, findings, filter, viewingOlder }: Props) {
  const [open, setOpen] = useState<Set<string>>(new Set())
  const shown = filter ? findings.filter(f => f.severity === filter) : findings

  const toggle = (key: string) => {
    const next = new Set(open)
    if (next.has(key)) next.delete(key)
    else next.add(key)
    setOpen(next)
  }

  return (
    <section className="panel" style={{ ['--i' as string]: 10 }}>
      <h2>
        Current run findings{' '}
        <span className="muted">
          {run ? `· ${run.commit_sha.slice(0, 8)}` : ''}
          {viewingOlder ? ' (older run — click its feed entry again to return)' : ''}
          {filter ? ` · ${filter} only` : ''}
        </span>
      </h2>

      {shown.length === 0 ? (
        <p className="empty">{run ? 'Nothing to show for this selection.' : 'No runs on this branch yet.'}</p>
      ) : (
        shown.map((f, i) => {
          const key = `${f.file}:${f.line}:${f.tool}:${f.ruleID}:${i}`
          const isOpen = open.has(key)
          return (
            <div className="finding" key={key}>
              <button className="head" aria-expanded={isOpen} onClick={() => toggle(key)}>
                <span className="sev">
                  <i style={{ background: SEVERITY_COLOR[f.severity] }} />
                  {f.severity}
                </span>
                <code title={f.file}>{f.file}{f.line ? `:${f.line}` : ''}</code>
                <span className="msg">{f.message}</span>
                <span className="chev" aria-hidden>›</span>
              </button>
              <div className={isOpen ? 'detail open' : 'detail'}>
                <div>
                  <div className="body">
                    <code className="meta">{f.tool}:{f.ruleID} · {f.category}</code>
                    {f.explanation || 'No explanation — this run was analysed without an LLM provider.'}
                  </div>
                </div>
              </div>
            </div>
          )
        })
      )}
    </section>
  )
}
```

- [ ] **Step 14: Mount them in `App.tsx` and wire drill-down**

Add the imports, then replace the Task 9 comment placeholder:

```tsx
import { ToolStatusPanel } from './components/ToolStatusPanel'
import { TopFiles } from './components/TopFiles'
import { ActivityFeed } from './components/ActivityFeed'
import { FindingsTable } from './components/FindingsTable'
```

```tsx
          <div className="split">
            <ToolStatusPanel tools={data.current?.run.tools ?? null} />
            <TopFiles files={data.current?.top_files ?? null} />
          </div>
          <ActivityFeed history={data.history} onSelectRun={selectRun} />
          <FindingsTable
            run={viewing ? viewing.run : (data.current?.run ?? null)}
            findings={viewing ? viewing.findings : (data.current?.findings ?? [])}
            filter={filter}
            viewingOlder={viewing !== null}
          />
```

And add the drill-down handler above the `return`:

```tsx
  // Loading an older run swaps only the findings table; clicking the run that
  // is already shown returns to the current run.
  const selectRun = useCallback(async (runID: number) => {
    if (!data?.current) return
    if (viewing?.run.id === runID || runID === data.current.run.id) {
      setViewing(null)
      return
    }
    try {
      setViewing(await fetchJSON<RunDetail>(`/api/runs/${runID}`, token))
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) signOut('That token was rejected.')
    }
  }, [data, viewing, token, signOut])
```

- [ ] **Step 15: Write the component tests**

`src/components/__tests__/FindingsTable.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FindingsTable } from '../FindingsTable'
import { current, finding } from '../../test/fixtures'

const run = current().run

test('a row expands to its explanation and collapses again', async () => {
  render(<FindingsTable run={run} findings={[finding()]} filter={null} viewingOlder={false} />)
  const row = screen.getByRole('button', { expanded: false })

  await userEvent.click(row)
  expect(screen.getByRole('button', { expanded: true })).toBeInTheDocument()
  expect(screen.getByText(/leaks secrets in the binary/)).toBeInTheDocument()

  await userEvent.click(row)
  expect(screen.getByRole('button', { expanded: false })).toBeInTheDocument()
})

test('the severity filter hides everything else', () => {
  const findings = [finding({ severity: 'high' }), finding({ severity: 'low', file: 'z.go' })]
  render(<FindingsTable run={run} findings={findings} filter="low" viewingOlder={false} />)

  expect(screen.getByText('z.go:4')).toBeInTheDocument()
  expect(screen.queryByText('a.go:4')).not.toBeInTheDocument()
  expect(screen.getByText(/low only/)).toBeInTheDocument()
})

test('a finding with no explanation says so instead of showing a blank panel', async () => {
  render(<FindingsTable run={run} findings={[finding({ explanation: '' })]} filter={null} viewingOlder={false} />)
  await userEvent.click(screen.getByRole('button'))
  expect(screen.getByText(/analysed without an LLM provider/)).toBeInTheDocument()
})

test('viewing an older run is stated in the heading', () => {
  render(<FindingsTable run={run} findings={[finding()]} filter={null} viewingOlder />)
  expect(screen.getByText(/older run/)).toBeInTheDocument()
})
```

`src/components/__tests__/ToolStatusPanel.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { ToolStatusPanel } from '../ToolStatusPanel'

test('a skipped tool states its reason in text, not only in color', () => {
  render(<ToolStatusPanel tools={[
    { name: 'gosec', skipped: false },
    { name: 'clippy', skipped: true, error: 'install failed' },
  ]} />)

  expect(screen.getByText('ran')).toBeInTheDocument()
  expect(screen.getByText(/skipped — install failed/)).toBeInTheDocument()
})

test('the pipeline diagram describes which tools ran for a screen reader', () => {
  render(<ToolStatusPanel tools={[
    { name: 'gosec', skipped: false },
    { name: 'clippy', skipped: true, error: 'install failed' },
  ]} />)

  expect(screen.getByRole('img', { name: /gosec ran, clippy skipped/ })).toBeInTheDocument()
})

test('a branch with no runs renders a plain message', () => {
  render(<ToolStatusPanel tools={null} />)
  expect(screen.getByText(/No runs on this branch yet/)).toBeInTheDocument()
})
```

- [ ] **Step 16: Run everything and rebuild the bundle**

```bash
cd internal/dashboard/web/ui && npm test && npm run build
```

Expected: all component tests PASS, build clean.

Run: `go test ./... -count=1 && go build ./... && go vet ./... && gofmt -l .`
Expected: PASS and no output.

- [ ] **Step 17: Look at the page**

With seeded data including one skipped tool (hand-craft a push with `"tools":[{"name":"gosec","skipped":false},{"name":"clippy","skipped":true,"error":"install failed"}]`), confirm:
- the tool list names each tool and its reason, skipped ones struck through;
- the pipeline diagram draws the skipped tool dashed with no outbound edge;
- top files rank by count with proportional bars and full paths on hover;
- the activity feed lists recent runs newest-first with `+new / −fixed` and a health pill;
- clicking a feed entry loads that older run into the findings table and says so; clicking it again returns;
- clicking a severity tile filters the table and the heading names the filter;
- a findings row expands smoothly to its explanation and the chevron rotates.

- [ ] **Step 18: Hand back to the user**

Do NOT run git commands. Report the files created/modified (including the rebuilt `dist/`) and stop so the user can commit.

---

### Task 10: End-to-end smoke test, Docker image, docker-compose, quickstart

**Files:**
- Create: `internal/dashboard/e2e_test.go`
- Create: `Dockerfile`
- Create: `docker-compose.yml`
- Create: `.dockerignore`
- Create: `docs/dashboard.md`
- Modify: `.gitignore` (ignore `node_modules/` and `.env`; **do not** ignore `internal/dashboard/web/dist/`)

**Interfaces:**
- Consumes: everything. This task adds no new exported API.

- [ ] **Step 1: Write the end-to-end smoke test**

`internal/dashboard/e2e_test.go` — the only test that drives the CLI's own push client against the real API and store, so it catches wiring bugs the per-package tests structurally cannot.

```go
package dashboard_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"testing/fstest"

	"codebase-analyser/internal/dashboard/api"
	"codebase-analyser/internal/dashboard/store"
	"codebase-analyser/internal/push"
)

const adminToken = "e2e-admin"

func TestCLIPushToDashboardRoundTrip(t *testing.T) {
	dsn := os.Getenv("DASHBOARD_TEST_DSN")
	if dsn == "" {
		t.Skip("DASHBOARD_TEST_DSN not set, skipping dashboard e2e test")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	if err := st.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}

	srv := httptest.NewServer(api.New(st, adminToken, fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")},
	}))
	defer srv.Close()

	repo, token, err := st.CreateRepo(ctx, "git@github.com:acme/widgets.git")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}

	// Push two runs through the CLI's own client, not hand-rolled HTTP.
	first := []byte(`{"summary":{"critical":0,"high":2,"medium":0,"low":0},"findings":[
		{"file":"a.go","line":1,"tool":"gosec","ruleID":"G101","category":"security","severity":"high","message":"hardcoded credential","explanation":"leaks secrets"},
		{"file":"b.go","line":7,"tool":"gosec","ruleID":"G104","category":"correctness","severity":"high","message":"unhandled error","explanation":"hides failures"}]}`)
	second := []byte(`{"summary":{"critical":1,"high":1,"medium":0,"low":0},"findings":[
		{"file":"a.go","line":3,"tool":"gosec","ruleID":"G101","category":"security","severity":"high","message":"hardcoded credential","explanation":"leaks secrets"},
		{"file":"c.go","line":9,"tool":"clippy","ruleID":"unwrap_used","category":"correctness","severity":"critical","message":"unwrap on Result","explanation":"panics in production"}]}`)

	tools := []push.ToolStatus{{Name: "gosec"}, {Name: "clippy", Skipped: true, Error: "install failed"}}
	if err := push.Send(ctx, srv.URL, token, "main", "commit-one", tools, first); err != nil {
		t.Fatalf("push run 1: %v", err)
	}
	if err := push.Send(ctx, srv.URL, token, "main", "commit-two", tools, second); err != nil {
		t.Fatalf("push run 2: %v", err)
	}
	// A CI retry of the second commit must not create a third run.
	if err := push.Send(ctx, srv.URL, token, "main", "commit-two", tools, second); err != nil {
		t.Fatalf("re-push run 2: %v", err)
	}

	req, _ := http.NewRequest("GET", srv.URL+"/api/repos/"+strconv.FormatInt(repo.ID, 10)+"/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET dashboard: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status %d", resp.StatusCode)
	}

	var got struct {
		Branch   string `json:"branch"`
		Branches []struct {
			Name string `json:"name"`
		} `json:"branches"`
		History []struct {
			CommitSHA string         `json:"commit_sha"`
			Counts    map[string]int `json:"counts"`
			New       int            `json:"new"`
			Fixed     int            `json:"fixed"`
		} `json:"history"`
		Current struct {
			New        int            `json:"new"`
			Fixed      int            `json:"fixed"`
			Categories map[string]int `json:"categories"`
			Findings   []struct {
				Tool        string `json:"tool"`
				RuleID      string `json:"ruleID"`
				Explanation string `json:"explanation"`
			} `json:"findings"`
			Run struct {
				Tools []struct {
					Name    string `json:"name"`
					Skipped bool   `json:"skipped"`
				} `json:"tools"`
			} `json:"run"`
		} `json:"current"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}

	if got.Branch != "main" || len(got.Branches) != 1 {
		t.Errorf("branch=%q branches=%v, want main only", got.Branch, got.Branches)
	}
	if len(got.History) != 2 {
		t.Fatalf("history has %d runs, want 2 (the retry must have overwritten, not appended)", len(got.History))
	}
	if got.History[0].CommitSHA != "commit-one" || got.History[1].CommitSHA != "commit-two" {
		t.Errorf("history order = %s,%s, want oldest first", got.History[0].CommitSHA, got.History[1].CommitSHA)
	}
	if got.History[1].Counts["critical"] != 1 || got.History[1].Counts["high"] != 1 {
		t.Errorf("second run counts = %v, want critical=1 high=1 recounted from findings", got.History[1].Counts)
	}
	if got.Current.New != 1 || got.Current.Fixed != 1 {
		t.Errorf("new=%d fixed=%d, want c.go new and b.go fixed (a.go moved line but is the same finding)",
			got.Current.New, got.Current.Fixed)
	}
	if got.Current.Categories["security"] != 1 || got.Current.Categories["correctness"] != 1 {
		t.Errorf("categories = %v", got.Current.Categories)
	}
	if len(got.Current.Findings) != 2 || got.Current.Findings[0].Explanation == "" {
		t.Errorf("findings did not survive the round trip with explanations: %+v", got.Current.Findings)
	}
	if len(got.Current.Run.Tools) != 2 || !got.Current.Run.Tools[1].Skipped {
		t.Errorf("tool statuses = %+v, want clippy recorded as skipped", got.Current.Run.Tools)
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/dashboard/ -run TestCLIPushToDashboard -v -count=1`
Expected: PASS. A SKIP does not count — start the test Postgres first.

- [ ] **Step 3: Write `.dockerignore`**

The UI source must reach the build context (the image rebuilds `dist/` rather than trusting the committed copy), but `node_modules` and the local `dist` must not.

```
.git
.superpowers
docs
testdata
*.md
.env
analyser
dashboard
internal/dashboard/web/ui/node_modules
internal/dashboard/web/dist
```

- [ ] **Step 4: Update `.gitignore`**

Append:

```
node_modules/
.env
```

`internal/dashboard/web/dist/` is deliberately **not** ignored — `go build` cannot run npm, so the built bundle is a committed artifact.

- [ ] **Step 5: Write `Dockerfile`**

```dockerfile
# The frontend is rebuilt here rather than trusting the committed dist/, so
# the image can never ship a bundle that disagrees with the source in it.
FROM node:22-alpine AS ui
WORKDIR /ui
COPY internal/dashboard/web/ui/package*.json ./
RUN npm ci
COPY internal/dashboard/web/ui/ ./
RUN npm run build          # writes ../dist relative to /ui, i.e. /dist

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Replace the committed bundle with the one just built.
RUN rm -rf internal/dashboard/web/dist
COPY --from=ui /dist ./internal/dashboard/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dashboard ./cmd/dashboard

# Static binary, no shell, non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/dashboard /dashboard
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/dashboard"]
```

- [ ] **Step 6: Write `docker-compose.yml`**

```yaml
services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: analyser
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}
      POSTGRES_DB: analyser
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U analyser"]
      interval: 5s
      timeout: 3s
      retries: 10

  dashboard:
    build: .
    environment:
      DATABASE_URL: postgres://analyser:${POSTGRES_PASSWORD}@db:5432/analyser?sslmode=disable
      DASHBOARD_ADMIN_TOKEN: ${DASHBOARD_ADMIN_TOKEN:?set DASHBOARD_ADMIN_TOKEN in .env}
    ports:
      - "${DASHBOARD_PORT:-8080}:8080"
    depends_on:
      db:
        condition: service_healthy
    restart: unless-stopped

volumes:
  pgdata:
```

Both secrets use `:?` so a missing value fails `up` with a clear message instead of silently booting an unprotected dashboard.

- [ ] **Step 7: Write `docs/dashboard.md`**

````markdown
# Dashboard

Self-hosted web dashboard for `analyser` runs: trends, per-branch comparison
and full drill-down across every run CI has pushed.

## Run it

```bash
cat > .env <<'EOF'
POSTGRES_PASSWORD=<a strong password>
DASHBOARD_ADMIN_TOKEN=<a strong random token>
EOF
docker compose up -d --build
```

Open http://localhost:8080 and sign in with `DASHBOARD_ADMIN_TOKEN`.

## Register a repo

```bash
curl -sX POST localhost:8080/api/admin/repos \
  -H "Authorization: Bearer $DASHBOARD_ADMIN_TOKEN" \
  -d '{"remote_url":"git@github.com:acme/widgets.git"}'
```

The response contains the repo's ingest token. **It is shown once** — store it
as a CI secret. Lost tokens are replaced, not recovered:
`POST /api/admin/repos/<id>/token`.

## Push from CI

```bash
analyser run . --dashboard-url https://dashboard.internal --dashboard-token "$ANALYSER_DASHBOARD_TOKEN"
```

Branch, commit and remote are read from the checkout. If the dashboard is
unreachable the CLI prints a warning and keeps its normal exit code — a
dashboard outage never fails a build.

## Working on the frontend

```bash
cd internal/dashboard/web/ui
npm install
npm run dev     # Vite on :5173, proxying /api to a dashboard on :8080
npm test        # component tests
npm run build   # writes ../dist — COMMIT THE RESULT
```

`internal/dashboard/web/dist/` is a committed build artifact: `go build`
cannot run npm, so the binary embeds whatever is checked in. Rebuild and
commit it with any UI change.

## Notes

- Every branch that pushes is stored and viewable; the UI opens on `main`.
- Re-pushing the same commit overwrites that run rather than duplicating it.
- Retention is unlimited; there is no pruning job.
- The admin token gates the UI and all repo management. A repo's ingest token
  can only write that repo's runs.
````

- [ ] **Step 8: Verify the container actually works**

```bash
printf 'POSTGRES_PASSWORD=devpass\nDASHBOARD_ADMIN_TOKEN=devtoken\n' > .env
docker compose up -d --build
sleep 10
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/                 # want 200
curl -sX POST localhost:8080/api/admin/repos -H 'Authorization: Bearer devtoken' \
  -d '{"remote_url":"github.com/acme/widgets"}'                          # want a token
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/api/admin/repos   # want 401, no token
docker compose down
rm .env
```

Expected: 200 for the UI (a real Vite `index.html`, not a placeholder), a JSON token from registration, 401 unauthenticated.

**Note:** this machine's Docker cannot create bridge networks (`failed to add the host … pair interfaces: operation not supported`), which is why the test Postgres runs with `--network host`. If `docker compose up` hits the same error, verify the image builds (`docker compose build`) and run the binary directly against the test Postgres instead, recording in the report that compose networking is unverifiable here.

- [ ] **Step 9: Full verification**

```bash
go test ./... -count=1 && go build ./... && go vet ./... && gofmt -l .
cd internal/dashboard/web/ui && npm test && npm run build
```

Expected: PASS across every Go package with no SKIPs for a missing `DASHBOARD_TEST_DSN`, no output from build/vet/gofmt, and all component tests passing.

- [ ] **Step 10: Hand back to the user**

Do NOT run git commands. Report every file created and stop so the user can commit.

---

## Spec coverage

| Spec requirement | Task |
|---|---|
| Single Go binary, API + embedded SPA, Postgres | 1, 5, 7 |
| Docker image + docker-compose | 10 |
| CLI `--dashboard-url` + ingest token, git metadata auto-detected | 6 |
| Push is best-effort, never fails CI or changes exit code | 6 (step 9 proves it) |
| Re-push of the same commit overwrites | 2 (upsert), 10 (e2e) |
| All branches ingest and are viewable; UI defaults to the default branch | 2, 3 (`PickDefaultBranch`), 5, 7 |
| Unlimited retention, no pruning | by omission — nothing deletes runs |
| Admin token env var gates UI + repo management | 4, 5, 7 |
| Per-repo ingest token, shown once, scoped to its repo | 1, 4 |
| Data model: repos / runs / findings | 1 |
| API: register, list, regenerate/revoke, ingest, history, run detail | 4, 5 (see Deviations 1-2) |
| View 1 Top bar | 7 |
| View 2 All branches | 7 |
| View 3 Severity summary cards (count, delta, sparkline, click-to-filter) | 7 (tiles), 9 (filter applies in the table) |
| View 4 Findings-over-time chart (hover readout, legend toggle, peak callout) | 8 |
| View 5 Health score | 3 (formula), 8 (hero tile — see Deviation 4 note on form) |
| View 6 Findings by category | 3, 8 |
| View 7 Since last run | 3 (`Diff`), 8 |
| View 8 Tool run status | 6 (capture), 9 (display) |
| View 9 Top offending files | 3, 9 |
| View 10 Recent activity feed | 9 |
| View 11 Analysis pipeline diagram | 9 |
| View 12 Findings table with inline explanations | 9 |
| Deliberate motion, two easing curves | 7 (`--ease`, `--spring`), 8, 9 |
| Testing: per-endpoint unit tests on real Postgres | 1, 2, 4, 5 |
| Testing: explicit upsert-by-commit test | 2 (plus a step proving it can fail) |
| Testing: frontend component tests for the interactive pieces | 7 (branch select, severity filter), 8 (chart legend toggle, table view), 9 (row expand, tool status) |
| Testing: e2e push of two runs for a fixture repo | 10 |

---

## Execution Handoff

Executed with superpowers:subagent-driven-development — fresh implementer per task, task review after each, and a hand-back to the user for committing between tasks (standing instruction: the controller and its implementers run no git commands).




