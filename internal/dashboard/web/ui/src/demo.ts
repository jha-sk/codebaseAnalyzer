import type {
  Branch, Counts, CurrentRun, DashboardData, FileCount, Finding, HistoryPoint, Repo, Run,
} from './types'

// Sentinel repo id for demo mode. Always negative so it can never collide
// with a real (auto-increment, positive) repo id from the API - App.tsx uses
// that to recognise "we are showing the demo" and skip fetching for it.
export const DEMO_REPO_ID = -1

const DAY = 86_400_000
const HOUR = 3_600_000

function isoAgo(ms: number): string {
  return new Date(Date.now() - ms).toISOString()
}

// Mirrors the server's weighting exactly (internal/dashboard/api/metrics.go
// HealthScore/penalty) so a demo health figure can never contradict its own
// counts. Duplicated rather than imported because the Go formula has no TS
// twin to share - this is the whole formula, three lines.
const SEVERITY_WEIGHT: Counts = { critical: 12, high: 6, medium: 2, low: 1 }

function penalty(c: Counts): number {
  return c.critical * SEVERITY_WEIGHT.critical + c.high * SEVERITY_WEIGHT.high
    + c.medium * SEVERITY_WEIGHT.medium + c.low * SEVERITY_WEIGHT.low
}

function healthScore(cur: Counts, prev: Counts | null): number {
  let score = 100 - penalty(cur)
  if (prev) {
    const curP = penalty(cur)
    const prevP = penalty(prev)
    if (curP < prevP) score += 5
    else if (curP > prevP) score -= 5
  }
  return Math.max(0, Math.min(100, score))
}

function tallyCounts(findings: Finding[]): Counts {
  const out: Counts = { critical: 0, high: 0, medium: 0, low: 0 }
  for (const f of findings) out[f.severity]++
  return out
}

const CATEGORIES = ['correctness', 'concurrency', 'security', 'operational'] as const

function tallyCategories(findings: Finding[]): Record<string, number> {
  const out: Record<string, number> = {}
  for (const c of CATEGORIES) out[c] = 0
  for (const f of findings) if (f.category in out) out[f.category]++
  return out
}

// Mirrors the server's TopFiles ranking (count desc, ties alphabetical) so
// the aggregate never disagrees with the finding list it was built from.
function topFiles(findings: Finding[], n: number): FileCount[] {
  const byFile = new Map<string, number>()
  for (const f of findings) byFile.set(f.file, (byFile.get(f.file) ?? 0) + 1)
  return [...byFile.entries()]
    .map(([file, count]) => ({ file, count }))
    .sort((a, b) => (b.count - a.count) || a.file.localeCompare(b.file))
    .slice(0, n)
}

// Findings for the current (latest) run on main. Fictional paths only, and
// every severity/category represented so the findings table, severity
// cards and category bars all have something real to show - this doubles as
// a showcase of what the LLM explanations look like.
const CURRENT_FINDINGS: Finding[] = [
  {
    file: 'internal/example/handler.go', line: 42, tool: 'gosec', ruleID: 'G106',
    category: 'security', severity: 'critical',
    message: 'SQL query built by concatenating request input directly into the statement string',
    explanation: 'An attacker who controls the request parameter can inject arbitrary SQL, altering the query’s meaning to read or modify data outside the intended scope. Use parameterized queries (?, $1) or a query builder that binds values, never string-concatenate untrusted input into SQL.',
    fixPattern: 'db.QueryContext(ctx, "SELECT * FROM users WHERE id = $1", userID)',
  },
  {
    file: 'internal/example/handler.go', line: 88, tool: 'gosec', ruleID: 'G304',
    category: 'security', severity: 'high',
    message: 'file path passed to os.Open is derived from unsanitized user input',
    explanation: 'A path like ../../etc/passwd let through this handler would let a caller read files outside the intended directory. Resolve the path with filepath.Clean, then verify it is still inside the allowed base directory before opening it.',
    fixPattern: 'clean := filepath.Clean(filepath.Join(baseDir, userPath))\nif !strings.HasPrefix(clean, baseDir) { return errInvalidPath }',
  },
  {
    file: 'internal/example/worker/pool.go', line: 117, tool: 'golangci-lint', ruleID: 'SA2001',
    category: 'concurrency', severity: 'high',
    message: 'mutex is locked and unlocked with no guarded access in between (empty critical section)',
    explanation: 'An empty critical section usually means a lock that was meant to guard a read or write got separated from it during a refactor, so the field it protects is now touched outside the lock elsewhere - a race waiting to happen. Move the guarded access back inside the Lock/Unlock pair, or delete the lock if it truly guards nothing.',
    fixPattern: 'mu.Lock()\np.queue = append(p.queue, item)\nmu.Unlock()',
  },
  {
    file: 'go.mod', line: 12, tool: 'govulncheck', ruleID: 'GO-2024-2963',
    category: 'security', severity: 'medium',
    message: 'golang.org/x/net is affected by a known denial-of-service vulnerability and is called from a reachable code path',
    explanation: 'govulncheck traced a call path from this binary into the vulnerable function, so this is not just a transitive dependency risk - it is exploitable through this code as written. Upgrade golang.org/x/net to the patched version named in the advisory.',
    fixPattern: 'go get golang.org/x/net@v0.23.0 && go mod tidy',
  },
  {
    file: 'internal/example/auth/middleware.go', line: 29, tool: 'golangci-lint', ruleID: 'SA4006',
    category: 'correctness', severity: 'medium',
    message: 'value assigned to err is never used before being overwritten on the next line',
    explanation: 'The first assignment is dead - either an error check was meant to happen here and was accidentally dropped, or the assignment itself is pointless. Either way it hides a failure path from the caller. Check the error immediately after the call that produced it.',
    fixPattern: 'if err := token.Validate(); err != nil {\n    return fmt.Errorf("validate token: %w", err)\n}',
  },
  {
    file: 'src/worker.rs', line: 63, tool: 'clippy', ruleID: 'clippy::significant_drop_in_scrutinee',
    category: 'concurrency', severity: 'medium',
    message: 'mutex guard is held across an await point inside a match scrutinee, blocking other tasks for the duration',
    explanation: 'Holding a std::sync::Mutex guard across .await stalls every other task waiting on that lock for as long as the awaited future takes, which can serialize work that was meant to run concurrently. Clone the guarded value out of the lock before awaiting, or switch to an async-aware mutex.',
    fixPattern: 'let value = { let guard = mutex.lock().unwrap(); guard.clone() };\nvalue.process().await;',
  },
  {
    file: 'internal/example/handler.go', line: 15, tool: 'golangci-lint', ruleID: 'noctx',
    category: 'operational', severity: 'medium',
    message: 'http.Get is called without a context, so the request cannot be cancelled or given a deadline',
    explanation: 'Without a context carrying a timeout, a slow or hung upstream can block this handler indefinitely and tie up a goroutine per stuck request. Use http.NewRequestWithContext with a context that has a deadline, and pass it through client.Do.',
    fixPattern: 'req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)\nresp, err := client.Do(req)',
  },
  {
    file: 'internal/example/auth/middleware.go', line: 54, tool: 'golangci-lint', ruleID: 'bodyclose',
    category: 'operational', severity: 'low',
    message: 'response body from client.Do is never closed on the error-handling path',
    explanation: 'Leaving the body unclosed on this path leaks the underlying connection back to the pool as unusable, and under sustained traffic this exhausts file descriptors. Add a deferred resp.Body.Close() immediately after the error check.',
    fixPattern: 'resp, err := client.Do(req)\nif err != nil { return err }\ndefer resp.Body.Close()',
  },
  {
    file: 'src/cache/lru.rs', line: 21, tool: 'clippy', ruleID: 'clippy::redundant_clone',
    category: 'correctness', severity: 'low',
    message: 'value is cloned here but the original is never used afterward',
    explanation: 'The clone costs an allocation and a copy for no behavioral benefit, since ownership could simply move instead. Not a bug on its own, but on a hot path like a cache lookup the extra allocation is measurable. Drop the .clone() and move the value instead.',
    fixPattern: 'let entry = self.map.remove(&key).unwrap();',
  },
  {
    file: 'src/cache/lru.rs', line: 47, tool: 'cargo-audit', ruleID: 'RUSTSEC-2023-0044',
    category: 'security', severity: 'low',
    message: 'dependency `time` 0.1.43 is affected by a segfault in unsound time zone handling',
    explanation: 'The advisory describes undefined behavior when the process reads the local time zone from multiple threads, which can crash the process rather than merely misbehave. Upgrade the `time` crate past 0.2.23, or pin to a version that backports the fix.',
    fixPattern: 'cargo update -p time --precise 0.3.36',
  },
]

// Four earlier runs on main, oldest first. Only counts are needed here (the
// trend chart, sparklines and activity feed all read off HistoryPoint, never
// off a per-run finding list) - counts genuinely rise then fall so the chart
// draws a real shape and one run stands out as the busiest.
const EARLIER_COUNTS: Counts[] = [
  { critical: 1, high: 3, medium: 5, low: 4 },   // oldest
  { critical: 2, high: 4, medium: 6, low: 5 },   // worsening
  { critical: 3, high: 6, medium: 7, low: 6 },   // busiest run
  { critical: 1, high: 4, medium: 5, low: 5 },   // recovering
]

// new/fixed per point, chosen so new-fixed always matches the change in
// total findings between consecutive runs (the first run has no previous
// run, so everything in it counts as new).
const EARLIER_DELTAS: Array<{ readonly new: number; readonly fixed: number }> = [
  { new: 13, fixed: 0 },
  { new: 6, fixed: 2 },
  { new: 8, fixed: 3 },
  { new: 2, fixed: 9 },
]

const EARLIER_SHAS = ['a10f2c9de001', 'b21e3d8cf102', 'c32d4e7be203', 'd43c5f6ad304']
const EARLIER_AGO_MS = [13 * DAY, 9 * DAY, 6 * DAY, 3 * DAY]
const EARLIER_RUN_IDS = [101, 102, 103, 104]

const CURRENT_SHA = 'e54b60f9c405'
const CURRENT_RUN_ID = 105
const CURRENT_AGO_MS = 5 * HOUR

/** A complete, self-consistent demo payload for a fresh install with nothing pushed yet. */
export function demoDashboard(): DashboardData {
  const currentCounts = tallyCounts(CURRENT_FINDINGS)
  const allCounts = [...EARLIER_COUNTS, currentCounts]
  const allDeltas = [...EARLIER_DELTAS, { new: 1, fixed: 6 }]
  const allShas = [...EARLIER_SHAS, CURRENT_SHA]
  const allAgo = [...EARLIER_AGO_MS, CURRENT_AGO_MS]
  const allRunIDs = [...EARLIER_RUN_IDS, CURRENT_RUN_ID]

  const history: HistoryPoint[] = allCounts.map((counts, i) => ({
    run_id: allRunIDs[i],
    commit_sha: allShas[i],
    pushed_at: isoAgo(allAgo[i]),
    counts,
    health: healthScore(counts, i > 0 ? allCounts[i - 1] : null),
    new: allDeltas[i].new,
    fixed: allDeltas[i].fixed,
  }))

  const previousCounts = EARLIER_COUNTS[EARLIER_COUNTS.length - 1]
  const deltas: Counts = {
    critical: currentCounts.critical - previousCounts.critical,
    high: currentCounts.high - previousCounts.high,
    medium: currentCounts.medium - previousCounts.medium,
    low: currentCounts.low - previousCounts.low,
  }

  const run: Run = {
    id: CURRENT_RUN_ID,
    repo_id: DEMO_REPO_ID,
    branch: 'main',
    commit_sha: CURRENT_SHA,
    pushed_at: isoAgo(CURRENT_AGO_MS),
    counts: currentCounts,
    tools: [
      { name: 'golangci-lint', skipped: false },
      { name: 'gosec', skipped: false },
      { name: 'govulncheck', skipped: false },
      { name: 'clippy', skipped: false },
      { name: 'cargo-audit', skipped: false },
      { name: 'eslint', skipped: true, error: 'no package.json found in repository root; skipping JavaScript/TypeScript checks' },
    ],
  }

  const current: CurrentRun = {
    run,
    health: healthScore(currentCounts, previousCounts),
    deltas,
    new: allDeltas[allDeltas.length - 1].new,
    fixed: allDeltas[allDeltas.length - 1].fixed,
    categories: tallyCategories(CURRENT_FINDINGS),
    top_files: topFiles(CURRENT_FINDINGS, 10),
    findings: CURRENT_FINDINGS,
  }

  const mainBranch: Branch = {
    name: 'main',
    last_run_at: run.pushed_at,
    commit_sha: run.commit_sha,
    run_id: run.id,
    counts: currentCounts,
  }
  const featureBranch: Branch = {
    name: 'feature/rate-limit',
    last_run_at: isoAgo(DAY + 4 * HOUR),
    commit_sha: 'f6593a1eb506',
    run_id: 106,
    counts: { critical: 0, high: 1, medium: 3, low: 2 },
  }

  const repo: Repo = {
    id: DEMO_REPO_ID,
    remote_url: 'demo/example-repo',
    registered_at: isoAgo(EARLIER_AGO_MS[0]),
  }

  return {
    repo,
    branch: 'main',
    branches: [mainBranch, featureBranch],
    history,
    current,
  }
}
