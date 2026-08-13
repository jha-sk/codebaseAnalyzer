import { Bars } from './Bars'
import type { FileCount } from '../types'

export function TopFiles({ files }: { files: FileCount[] | null }) {
  return (
    <section className="panel" style={{ ['--i' as string]: 8 }}>
      <h2>Top offending files</h2>
      {!files ? (
        <p className="muted">No runs on this branch yet.</p>
      ) : files.length === 0 ? (
        <p className="empty">No findings in this run.</p>
      ) : (
        <Bars data={files.map(f => ({ name: shorten(f.file), value: f.count, title: f.file }))} />
      )}
    </section>
  )
}

// Long paths get their tail kept - the filename is what identifies it.
function shorten(path: string): string {
  return path.length <= 34 ? path : '…' + path.slice(-33)
}
