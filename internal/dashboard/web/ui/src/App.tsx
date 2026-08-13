import { useCallback, useEffect, useState } from 'react'
import { ApiError, fetchJSON } from './api'
import { relativeTime } from './theme'
import { TopBar } from './components/TopBar'
import { BranchTable } from './components/BranchTable'
import { SeverityCards } from './components/SeverityCards'
import type { DashboardData, Repo, RunDetail, Severity } from './types'

export default function App() {
  const [token, setToken] = useState(() => localStorage.getItem('analyser_token') ?? '')
  const [gateError, setGateError] = useState('')
  const [repos, setRepos] = useState<Repo[]>([])
  const [repoId, setRepoId] = useState<number | null>(null)
  const [branch, setBranch] = useState('')
  const [data, setData] = useState<DashboardData | null>(null)
  const [viewing, setViewing] = useState<RunDetail | null>(null)
  void viewing // read by Task 9's findings table; declared here so this compiles under noUnusedLocals now
  const [filter, setFilter] = useState<Severity | null>(null)
  const [status, setStatus] = useState('connecting')
  const [stale, setStale] = useState(false)

  const signOut = useCallback((message = '') => {
    localStorage.removeItem('analyser_token')
    setToken('')
    setData(null)
    setGateError(message)
  }, [])

  // Repo list: once per token.
  useEffect(() => {
    if (!token) return
    let cancelled = false
    fetchJSON<Repo[]>('/api/admin/repos', token)
      .then(list => {
        if (cancelled) return
        setRepos(list)
        setRepoId(list.length ? list[0].id : null)
        if (!list.length) { setStatus('no repos registered'); setStale(true) }
      })
      .catch(err => {
        if (err instanceof ApiError && err.status === 401) signOut('That token was rejected.')
        else { setStatus('load failed'); setStale(true) }
      })
    return () => { cancelled = true }
  }, [token, signOut])

  // Dashboard payload: whenever the repo or branch changes.
  useEffect(() => {
    if (!token || repoId === null) return
    let cancelled = false
    const query = branch ? `?branch=${encodeURIComponent(branch)}` : ''
    fetchJSON<DashboardData>(`/api/repos/${repoId}/dashboard${query}`, token)
      .then(payload => {
        if (cancelled) return
        setData(payload)
        setBranch(payload.branch)
        setViewing(null)   // never leave an unrelated run in the findings table
        setStale(false)
        setStatus(payload.current ? `live · ${relativeTime(payload.current.run.pushed_at)}` : 'no runs')
      })
      .catch(err => {
        if (err instanceof ApiError && err.status === 401) signOut('That token was rejected.')
        else { setStatus('load failed'); setStale(true) }
      })
    return () => { cancelled = true }
  }, [token, repoId, branch, signOut])

  if (!token) {
    return (
      <>
        <div className="bg" aria-hidden />
        <div className="gate">
          <form
            className="panel"
            onSubmit={e => {
              e.preventDefault()
              const value = new FormData(e.currentTarget).get('token')
              const next = String(value ?? '').trim()
              if (!next) return
              localStorage.setItem('analyser_token', next)
              setGateError('')
              setToken(next)
            }}
          >
            <h1>Codebase Analyser</h1>
            <p className="muted">Enter the dashboard admin token.</p>
            <input name="token" type="password" autoComplete="current-password"
                   placeholder="admin token" aria-label="admin token" required />
            <button type="submit">Unlock</button>
            {gateError && <p className="error" role="alert">{gateError}</p>}
          </form>
        </div>
      </>
    )
  }

  return (
    <>
      <div className="bg" aria-hidden />
      <TopBar
        repos={repos}
        repoId={repoId ?? 0}
        branches={data?.branches ?? []}
        branch={branch}
        status={status}
        stale={stale}
        onRepo={id => { setRepoId(id); setBranch(''); setFilter(null) }}
        onBranch={name => { setBranch(name); setFilter(null) }}
        onSignOut={() => signOut()}
      />
      {data && (
        <>
          <BranchTable
            branches={data.branches}
            selected={branch}
            onSelect={name => { setBranch(name); setFilter(null) }}
          />
          <SeverityCards
            current={data.current}
            history={data.history}
            filter={filter}
            onFilter={setFilter}
          />
          {/* Task 8 inserts TrendChart, HealthTile, CategoryBars, SinceLastRun here. */}
          {/* Task 9 inserts ToolStatusPanel, TopFiles, ActivityFeed, FindingsTable here. */}
        </>
      )}
    </>
  )
}
