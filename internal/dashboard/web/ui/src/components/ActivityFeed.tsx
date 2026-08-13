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
