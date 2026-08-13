import type { ToolStatus } from '../types'

// The known tools, in the CLI's real pipeline order - used only to sort the
// diagram, never to filter it. An adapter not in this list (a new language's
// linter, say) still gets drawn: it just sorts after the known ones, by name.
const PIPELINE_TOOLS = ['golangci-lint', 'gosec', 'govulncheck', 'clippy', 'cargo-audit']

/**
 * The CLI's real pipeline. A skipped tool is drawn disconnected - dashed box,
 * dashed inbound edge, no outbound edge - so the diagram shows a broken wire
 * rather than quietly omitting the stage.
 */
export function PipelineDiagram({ tools }: { tools: ToolStatus[] }) {
  const byName = new Map(tools.map(t => [t.name, t]))
  const known = PIPELINE_TOOLS.filter(name => byName.has(name))
  const unknown = tools.map(t => t.name).filter(name => !PIPELINE_TOOLS.includes(name)).sort()
  const active = [...known, ...unknown]
  if (!active.length) return null

  const rowH = 30
  const width = 620
  const height = Math.max(120, active.length * rowH + 30)
  const midY = height / 2

  const box = (x: number, y: number, label: string, off: boolean, w = 96) => (
    <g key={`${label}-${y}`}>
      <rect className={off ? 'node off' : 'node'} x={x} y={y - 12} width={w} height={24} rx={6} />
      <text className={off ? 'label off' : 'label'} x={x + w / 2} y={y + 4} textAnchor="middle">{label}</text>
    </g>
  )
  const edge = (fromX: number, fromY: number, toX: number, toY: number, off: boolean, key: string) => (
    <path key={key} className={off ? 'flow off' : 'flow'}
          d={`M${fromX} ${fromY} C${fromX + 26} ${fromY}, ${toX - 26} ${toY}, ${toX} ${toY}`} />
  )

  return (
    <div className="scroll-x">
      <svg className="pipe" viewBox={`0 0 ${width} ${height}`} width="100%" role="img"
           aria-label={`Analysis pipeline. ${active.map(n => `${n} ${byName.get(n)!.skipped ? 'skipped' : 'ran'}`).join(', ')}.`}>
        {box(6, midY, 'repository', false, 82)}
        {box(340, midY, 'normalize', false)}
        {box(452, midY, 'LLM explain', false)}
        {box(556, midY, 'report', false, 58)}
        {active.map((name, i) => {
          const y = midY - ((active.length - 1) * rowH) / 2 + i * rowH
          const skipped = byName.get(name)!.skipped
          return (
            <g key={name}>
              {edge(88, midY, 150, y, skipped, `in-${name}`)}
              {box(150, y, name, skipped, 150)}
              {!skipped && edge(300, y, 340, midY, false, `out-${name}`)}
            </g>
          )
        })}
        {edge(436, midY, 452, midY, false, 'normalize-explain')}
        {edge(548, midY, 556, midY, false, 'explain-report')}
      </svg>
    </div>
  )
}
