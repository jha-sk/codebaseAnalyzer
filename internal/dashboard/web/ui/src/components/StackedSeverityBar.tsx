import { SEVERITIES, SEVERITY_COLOR } from '../theme'
import type { Counts } from '../types'

/**
 * Composition at a glance: two branches with ten findings each look very
 * different if one of them is all critical. Segments are ordered
 * critical -> low so the ramp reads light-to-dark left-to-right.
 */
export function StackedSeverityBar({ counts }: { counts: Counts }) {
  const total = SEVERITIES.reduce((sum, s) => sum + counts[s], 0)
  const label = total === 0
    ? 'no findings'
    : SEVERITIES.filter(s => counts[s] > 0).map(s => `${counts[s]} ${s}`).join(', ')
  return (
    <div className="stack" role="img" aria-label={label} title={label}>
      {SEVERITIES.filter(s => counts[s] > 0).map(s => (
        <span key={s} style={{ flexGrow: counts[s], background: SEVERITY_COLOR[s] }} />
      ))}
    </div>
  )
}
