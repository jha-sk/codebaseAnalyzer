export type Severity = 'critical' | 'high' | 'medium' | 'low'
export type Counts = Record<Severity, number>

export interface Repo { id: number; remote_url: string; registered_at: string }

export interface Branch {
  name: string
  last_run_at: string
  commit_sha: string
  run_id: number
  counts: Counts
}

export interface ToolStatus { name: string; skipped: boolean; error?: string }

export interface Run {
  id: number
  repo_id: number
  branch: string
  commit_sha: string
  pushed_at: string
  counts: Counts
  tools: ToolStatus[]
}

export interface Finding {
  file: string
  line: number
  tool: string
  ruleID: string
  category: string
  severity: Severity
  message: string
  explanation: string
  fixPattern: string
}

export interface HistoryPoint {
  run_id: number
  commit_sha: string
  pushed_at: string
  counts: Counts
  health: number
  // Per-run delta vs. the previous run on this branch; the activity feed's
  // +new/-fixed figures read straight from these.
  new: number
  fixed: number
}

export interface FileCount { file: string; count: number }

export interface CurrentRun {
  run: Run
  health: number
  deltas: Counts
  new: number
  fixed: number
  categories: Record<string, number>
  top_files: FileCount[]
  findings: Finding[]
}

export interface DashboardData {
  repo: Repo
  branch: string
  branches: Branch[]
  history: HistoryPoint[]
  current: CurrentRun | null
}

export interface RunDetail { run: Run; findings: Finding[] }
