import { PipelineDiagram } from './PipelineDiagram'
import { STATUS } from '../theme'
import type { ToolStatus } from '../types'

/**
 * Makes the CLI's skip-and-continue behaviour visible. A tool that failed to
 * install contributes zero findings, and without this view that silence looks
 * like good news.
 */
export function ToolStatusPanel({ tools }: { tools: ToolStatus[] | null }) {
  return (
    <section className="panel" style={{ ['--i' as string]: 7 }}>
      <h2>Tool run status</h2>
      {!tools ? (
        <p className="muted">No runs on this branch yet.</p>
      ) : tools.length === 0 ? (
        <p className="muted">This run reported no tool statuses.</p>
      ) : (
        <>
          <div className="tools">
            {tools.map(tool => (
              <div className={tool.skipped ? 'tool skipped' : 'tool'} key={tool.name}>
                <i className="state" style={{ background: tool.skipped ? STATUS.critical : STATUS.good }} />
                <span className="name">{tool.name}</span>
                <span style={{ color: tool.skipped ? STATUS.critical : undefined }} className={tool.skipped ? '' : 'muted'}>
                  {tool.skipped ? `skipped — ${tool.error || 'no reason reported'}` : 'ran'}
                </span>
              </div>
            ))}
          </div>
          <h3>Analysis pipeline</h3>
          <PipelineDiagram tools={tools} />
        </>
      )}
    </section>
  )
}
