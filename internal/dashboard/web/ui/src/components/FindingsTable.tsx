import { useState } from 'react'
import { SEVERITY_COLOR } from '../theme'
import type { Finding, Run, Severity } from '../types'

interface Props {
  run: Run | null
  findings: Finding[]
  filter: Severity | null
  viewingOlder: boolean
}

/**
 * The CLI's human-readable report, made browsable. Rows expand inline to the
 * LLM explanation rather than replacing the page, so "what's true now" and
 * "how did we get here" stay visible together.
 */
export function FindingsTable({ run, findings, filter, viewingOlder }: Props) {
  const [open, setOpen] = useState<Set<string>>(new Set())
  const shown = filter ? findings.filter(f => f.severity === filter) : findings

  const toggle = (key: string) => {
    const next = new Set(open)
    if (next.has(key)) next.delete(key)
    else next.add(key)
    setOpen(next)
  }

  return (
    <section className="panel" style={{ ['--i' as string]: 10 }}>
      <h2>
        Current run findings{' '}
        <span className="muted">
          {run ? `· ${run.commit_sha.slice(0, 8)}` : ''}
          {viewingOlder ? ' (older run — click its feed entry again to return)' : ''}
          {filter ? ` · ${filter} only` : ''}
        </span>
      </h2>

      {shown.length === 0 ? (
        <p className="empty">{run ? 'Nothing to show for this selection.' : 'No runs on this branch yet.'}</p>
      ) : (
        shown.map((f, i) => {
          const key = `${f.file}:${f.line}:${f.tool}:${f.ruleID}:${i}`
          const isOpen = open.has(key)
          return (
            <div className="finding" key={key}>
              <button className="head" aria-expanded={isOpen} onClick={() => toggle(key)}>
                <span className="sev">
                  <i style={{ background: SEVERITY_COLOR[f.severity] }} />
                  {f.severity}
                </span>
                <code title={f.file}>{f.file}{f.line ? `:${f.line}` : ''}</code>
                <span className="msg">{f.message}</span>
                <span className="chev" aria-hidden>›</span>
              </button>
              <div className={isOpen ? 'detail open' : 'detail'}>
                <div>
                  <div className="body">
                    <code className="meta">{f.tool}:{f.ruleID} · {f.category}</code>
                    {f.explanation || 'No explanation — this run was analysed without an LLM provider.'}
                    {f.fixPattern && (
                      <div className="fix">
                        <span className="fix-label">Suggested fix</span>
                        <code className="fix-pattern">{f.fixPattern}</code>
                      </div>
                    )}
                  </div>
                </div>
              </div>
            </div>
          )
        })
      )}
    </section>
  )
}
