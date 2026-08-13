import type { Branch, Repo } from '../types'

interface Props {
  repos: Repo[]
  repoId: number
  branches: Branch[]
  branch: string
  status: string
  stale: boolean
  onRepo: (id: number) => void
  onBranch: (name: string) => void
  onSignOut: () => void
}

export function TopBar(props: Props) {
  return (
    <header className="topbar panel">
      <span className="brand">Codebase&nbsp;Analyser</span>
      <label className="pick">
        Repo
        <select value={props.repoId} onChange={e => props.onRepo(Number(e.target.value))}>
          {props.repos.map(r => <option key={r.id} value={r.id}>{r.remote_url}</option>)}
        </select>
      </label>
      <label className="pick">
        Branch
        <select value={props.branch} onChange={e => props.onBranch(e.target.value)}>
          {props.branches.map(b => <option key={b.name} value={b.name}>{b.name}</option>)}
        </select>
      </label>
      <span className={props.stale ? 'status stale' : 'status'}>
        <i className="dot" />{props.status}
      </span>
      <button className="ghost" onClick={props.onSignOut}>Sign out</button>
    </header>
  )
}
