import { StackedSeverityBar } from './StackedSeverityBar'
import { healthStatus, relativeTime } from '../theme'
import type { Branch, Counts } from '../types'

/**
 * Mirrors the server's weighting in api/metrics.go. The API scores only the
 * branch being viewed; this table summarises all of them, so the arithmetic
 * is repeated here. Keep the weights in sync.
 *
 * Deliberately omits the server's +-5 trend adjustment: that needs the
 * branch's previous run, which this table doesn't have (only latest counts
 * per branch). So the pill shown here for the selected branch can read a few
 * points off from `current.health` shown elsewhere for that same branch -
 * that's expected, not a bug.
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
                      tabIndex={0}
                      onClick={() => onSelect(b.name)}
                      onKeyDown={e => {
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault()
                          onSelect(b.name)
                        }
                      }}>
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
