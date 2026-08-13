import { Line, LineChart, ResponsiveContainer } from 'recharts'
import { CHART, healthStatus } from '../theme'
import type { HistoryPoint } from '../types'

/**
 * One number is a stat tile, not a gauge: a radial gauge is a one-bar bar
 * chart with extra geometry. Hero figure in the system sans with proportional
 * digits, its status color paired with a text label so the state never rides
 * on color alone.
 *
 * This figure is the server-computed `current.health`, which includes the
 * server's ±5 trend adjustment against the previous run. BranchTable's
 * client-side `branchHealth` deliberately omits that adjustment (it has no
 * previous run to compare against), so the same branch can show two slightly
 * different numbers on this page - by up to the size of the trend adjustment.
 * That is expected, not a bug.
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
