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
}

export interface HistoryPoint {
  run_id: number
  commit_sha: string
  pushed_at: string
  counts: Counts
  health: number
  // Sent by the server from Task 9 onward, when historyPoint gains New/Fixed
  // and the activity feed starts showing a per-run delta. Declared here ahead
  // of that so the type describes the finished contract; nothing reads these
  // before Task 9.
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
