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
