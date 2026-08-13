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
