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
                  // eslint-disable-next-line @typescript-eslint/no-explicit-any
                  label={(props: any) =>
                    props.index === rows.length - 1 ? (
                      <text x={Number(props.x ?? 0) + 8} y={Number(props.y ?? 0) + 4}
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
