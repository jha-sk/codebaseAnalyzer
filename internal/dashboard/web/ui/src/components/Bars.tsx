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
