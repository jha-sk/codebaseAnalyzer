import { demoDashboard } from '../../demo'
import { SEVERITIES } from '../../theme'
import type { Counts } from '../../types'

// Re-derives the server's health formula independently (internal/dashboard/
// api/metrics.go HealthScore) rather than reusing demo.ts's own copy, so
// this test actually catches a mistake in either implementation instead of
// just checking a function agrees with itself.
function expectedHealth(cur: Counts, prev: Counts | null): number {
  const penalty = (c: Counts) => c.critical * 12 + c.high * 6 + c.medium * 2 + c.low * 1
  let score = 100 - penalty(cur)
  if (prev) {
    const curP = penalty(cur)
    const prevP = penalty(prev)
    if (curP < prevP) score += 5
    else if (curP > prevP) score -= 5
  }
  return Math.max(0, Math.min(100, score))
}

test('current run counts match the severity tally of its findings', () => {
  const { current } = demoDashboard()
  expect(current).not.toBeNull()
  const tally: Counts = { critical: 0, high: 0, medium: 0, low: 0 }
  for (const f of current!.findings) tally[f.severity]++
  expect(current!.run.counts).toEqual(tally)
})

test('categories match the category tally of the findings', () => {
  const { current } = demoDashboard()
  const tally: Record<string, number> = { correctness: 0, concurrency: 0, security: 0, operational: 0 }
  for (const f of current!.findings) tally[f.category]++
  expect(current!.categories).toEqual(tally)
})

test('top_files counts match the file tally of the findings', () => {
  const { current } = demoDashboard()
  const tally = new Map<string, number>()
  for (const f of current!.findings) tally.set(f.file, (tally.get(f.file) ?? 0) + 1)
  for (const row of current!.top_files) {
    expect(row.count).toBe(tally.get(row.file))
  }
  const totalInTopFiles = current!.top_files.reduce((sum, r) => sum + r.count, 0)
  expect(totalInTopFiles).toBe(current!.findings.length)
})

test('the last history point equals the current run', () => {
  const { current, history } = demoDashboard()
  const last = history[history.length - 1]
  expect(last.counts).toEqual(current!.run.counts)
  expect(last.health).toBe(current!.health)
  expect(last.new).toBe(current!.new)
  expect(last.fixed).toBe(current!.fixed)
  expect(last.run_id).toBe(current!.run.id)
  expect(last.commit_sha).toBe(current!.run.commit_sha)
})

test('every history point health matches the documented formula for its counts', () => {
  const { history } = demoDashboard()
  history.forEach((point, i) => {
    const prev = i > 0 ? history[i - 1].counts : null
    expect(point.health).toBe(expectedHealth(point.counts, prev))
  })
})

test('history counts genuinely move both up and down, not monotonically', () => {
  const { history } = demoDashboard()
  const totals = history.map(p => p.counts.critical + p.counts.high + p.counts.medium + p.counts.low)
  const increasing = totals.every((v, i) => i === 0 || v >= totals[i - 1])
  const decreasing = totals.every((v, i) => i === 0 || v <= totals[i - 1])
  expect(increasing).toBe(false)
  expect(decreasing).toBe(false)
})

test('at least one tool is skipped and at least one is not', () => {
  const { current } = demoDashboard()
  const tools = current!.run.tools
  expect(tools.some(t => t.skipped)).toBe(true)
  expect(tools.some(t => !t.skipped)).toBe(true)
  // A skipped tool must carry a reason - that's the whole point of showing it.
  for (const tool of tools) {
    if (tool.skipped) expect(tool.error).toBeTruthy()
  }
})

test('all four severities and all four categories appear in the findings', () => {
  const { current } = demoDashboard()
  const severities = new Set(current!.findings.map(f => f.severity))
  const categories = new Set(current!.findings.map(f => f.category))
  for (const sev of SEVERITIES) expect(severities.has(sev)).toBe(true)
  for (const cat of ['correctness', 'concurrency', 'security', 'operational']) {
    expect(categories.has(cat)).toBe(true)
  }
})

test('two branches on main and a different one, main selected', () => {
  const data = demoDashboard()
  expect(data.branch).toBe('main')
  const names = data.branches.map(b => b.name)
  expect(names).toContain('main')
  expect(names.length).toBe(2)
  const [main, other] = data.branches
  expect(other.counts).not.toEqual(main.counts)
})

test('repo is an unmistakable placeholder, not a plausible name', () => {
  const { repo } = demoDashboard()
  expect(repo.remote_url).toBe('demo/example-repo')
})

test('demo findings use fictional file paths', () => {
  const { current } = demoDashboard()
  for (const f of current!.findings) {
    expect(f.file.startsWith('internal/example/') || f.file.startsWith('src/') || f.file === 'go.mod').toBe(true)
  }
})

test('pushed_at timestamps fall within the last two weeks and are not identical', () => {
  const { history } = demoDashboard()
  const now = Date.now()
  const twoWeeksMs = 14 * 24 * 60 * 60 * 1000
  const times = history.map(p => new Date(p.pushed_at).getTime())
  for (const t of times) {
    expect(t).toBeLessThanOrEqual(now)
    expect(t).toBeGreaterThan(now - twoWeeksMs)
  }
  expect(new Set(times).size).toBe(times.length)
})
