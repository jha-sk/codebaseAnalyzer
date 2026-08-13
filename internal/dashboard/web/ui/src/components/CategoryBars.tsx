import { Bars } from './Bars'

/**
 * Where the risk concentrates - information the severity tiles structurally
 * cannot show. Four fixed nominal categories, so: one series, one color.
 */
export function CategoryBars({ categories }: { categories: Record<string, number> | null }) {
  return (
    <section className="panel" style={{ ['--i' as string]: 5 }}>
      <h2>Findings by category</h2>
      {!categories ? (
        <p className="muted">No runs on this branch yet.</p>
      ) : (
        <Bars data={Object.entries(categories).map(([name, value]) => ({ name, value }))} />
      )}
    </section>
  )
}
