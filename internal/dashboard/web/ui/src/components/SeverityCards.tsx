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
