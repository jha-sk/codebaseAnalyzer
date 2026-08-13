import { useCallback, useEffect, useState } from 'react'
import { ApiError, fetchJSON } from './api'
import { relativeTime, STATUS } from './theme'
import { DEMO_REPO_ID, demoDashboard } from './demo'
import { TopBar } from './components/TopBar'
import { BranchTable } from './components/BranchTable'
import { SeverityCards } from './components/SeverityCards'
import { TrendChart } from './components/TrendChart'
import { HealthTile } from './components/HealthTile'
import { CategoryBars } from './components/CategoryBars'
import { SinceLastRun } from './components/SinceLastRun'
import { ToolStatusPanel } from './components/ToolStatusPanel'
import { TopFiles } from './components/TopFiles'
import { ActivityFeed } from './components/ActivityFeed'
import { FindingsTable } from './components/FindingsTable'
import type { DashboardData, Repo, RunDetail, Severity } from './types'

export default function App() {
  const [token, setToken] = useState(() => localStorage.getItem('analyser_token') ?? '')
  const [gateError, setGateError] = useState('')
  const [repos, setRepos] = useState<Repo[]>([])
  const [repoId, setRepoId] = useState<number | null>(null)
  const [branch, setBranch] = useState('')
  const [data, setData] = useState<DashboardData | null>(null)
  const [viewing, setViewing] = useState<RunDetail | null>(null)
  const [filter, setFilter] = useState<Severity | null>(null)
  const [status, setStatus] = useState('connecting')
  const [stale, setStale] = useState(false)
  const [isDemo, setIsDemo] = useState(false)

  const signOut = useCallback((message = '') => {
    localStorage.removeItem('analyser_token')
    setToken('')
    setData(null)
    setIsDemo(false)
    setGateError(message)
  }, [])

  // Substitutes the demo payload in place of real (absent) data. Also
  // replaces the repo list shown in the picker with the single demo repo -
  // this is a security dashboard, so a fake finding must never sit next to a
  // real, plausible-looking repository name.
  const enterDemoMode = useCallback(() => {
    const demo = demoDashboard()
    setRepos([demo.repo])
    setRepoId(DEMO_REPO_ID)
    setData(demo)
    setBranch(demo.branch)
    setIsDemo(true)
    setViewing(null)
    setStale(false)
    setStatus('demo data — no runs pushed yet')
  }, [])

  // Repo list: once per token.
  useEffect(() => {
    if (!token) return
    let cancelled = false
    fetchJSON<Repo[]>('/api/admin/repos', token)
      .then(list => {
        if (cancelled) return
        if (!list.length) { enterDemoMode(); return }
        setRepos(list)
        setRepoId(list[0].id)
      })
      .catch(err => {
        if (err instanceof ApiError && err.status === 401) signOut('That token was rejected.')
        else { setStatus('load failed'); setStale(true) }
      })
    return () => { cancelled = true }
  }, [token, signOut, enterDemoMode])

  // Dashboard payload: whenever the repo or branch changes. Skipped for the
  // demo repo id - there is nothing to fetch for it, enterDemoMode already
  // built the payload locally.
  useEffect(() => {
    if (!token || repoId === null || repoId === DEMO_REPO_ID) return
    let cancelled = false
    const query = branch ? `?branch=${encodeURIComponent(branch)}` : ''
    fetchJSON<DashboardData>(`/api/repos/${repoId}/dashboard${query}`, token)
      .then(payload => {
        if (cancelled) return
        const nothingToShow = payload.current === null && payload.branches.length === 0
        if (nothingToShow) { enterDemoMode(); return }
        setData(payload)
        setBranch(payload.branch)
        setIsDemo(false)
        setViewing(null)   // never leave an unrelated run in the findings table
        setStale(false)
        setStatus(payload.current ? `live · ${relativeTime(payload.current.run.pushed_at)}` : 'no runs')
      })
      .catch(err => {
        if (err instanceof ApiError && err.status === 401) signOut('That token was rejected.')
        else { setStatus('load failed'); setStale(true) }
      })
    return () => { cancelled = true }
  }, [token, repoId, branch, signOut, enterDemoMode])

  // Loading an older run swaps only the findings table; clicking the run that
  // is already shown returns to the current run.
  const selectRun = useCallback(async (runID: number) => {
    if (!data?.current) return
    if (viewing?.run.id === runID || runID === data.current.run.id) {
      setViewing(null)
      return
    }
    try {
      setViewing(await fetchJSON<RunDetail>(`/api/runs/${runID}`, token))
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) signOut('That token was rejected.')
    }
  }, [data, viewing, token, signOut])

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
      {isDemo && (
        <div className="demo-banner" role="alert" style={{ borderColor: STATUS.warning }}>
          <strong style={{ color: STATUS.warning }}>Demonstration data.</strong> No runs have been
          pushed to this dashboard yet. What you see below is example data so you can see how the
          dashboard looks. Push a real run
          with <code>analyser run . --dashboard-url &lt;this-url&gt; --dashboard-token &lt;token&gt;</code>.
        </div>
      )}
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
          <div className="split">
            <TrendChart history={data.history} />
            <HealthTile health={data.current?.health ?? null} history={data.history} />
          </div>
          <div className="split">
            <CategoryBars categories={data.current?.categories ?? null} />
            <SinceLastRun current={data.current} history={data.history} />
          </div>
          <div className="split">
            <ToolStatusPanel tools={data.current?.run.tools ?? null} />
            <TopFiles files={data.current?.top_files ?? null} />
          </div>
          <ActivityFeed history={data.history} onSelectRun={selectRun} />
          <FindingsTable
            run={viewing ? viewing.run : (data.current?.run ?? null)}
            findings={viewing ? viewing.findings : (data.current?.findings ?? [])}
            filter={filter}
            viewingOlder={viewing !== null}
          />
        </>
      )}
    </>
  )
}
