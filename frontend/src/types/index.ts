export interface User {
  id: string
  username: string
  createdAt?: string
}

export interface AuthResponse {
  token: string
  user: User
}

export interface Directory {
  path: string
  name: string
  stackCount: number
  isGitRepo: boolean
  gitBranch?: string
  gitRemote?: string
  scannedAt: string
}

export type StackStatus = 'running' | 'stopped' | 'partial' | 'unknown' | 'error'

export interface Container {
  id: string
  name: string
  image: string
  state: string
  status: string
  ports: PortBinding[]
  health?: string
}

export interface PortBinding {
  host: string
  container: string
  protocol: string
}

export interface ContainerMetrics {
  containerId: string
  name: string
  cpuPercent: number
  memUsage: number
  memLimit: number
  memPercent: number
  netRx: number
  netTx: number
  blockRead: number
  blockWrite: number
}

export interface Stack {
  id: string
  directory: string
  composeFile: string
  envFile?: string
  projectName: string
  status: StackStatus
  containerCount?: number
  isGitRepo: boolean
  gitBranch?: string
  gitCommit?: string
  gitDirty: boolean
  gitAhead: number
  gitBehind: number
  containers?: Container[]
}

export interface GitStatus {
  branch: string
  commit: string
  commitShort: string
  commitMessage: string
  commitAuthor: string
  commitDate: string
  dirty: boolean
  ahead: number
  behind: number
  remote: string
}

export interface GitCommit {
  hash: string
  short: string
  author: string
  email: string
  message: string
  date: string
}

export interface LintResult {
  level: 'error' | 'warning' | 'info'
  line?: number
  message: string
  rule: string
}

export interface EnvEntry {
  key: string
  value: string
  line?: number
  sensitive: boolean
  comment?: boolean
}

export interface CommandResult {
  status: string
  output: string
  duration_ms: number
}

export interface ApiError {
  error: string
  code: string
  details?: Record<string, unknown>
}

export interface ScanResult {
  directories: Directory[]
  scannedAt: string
  added: number
  removed: number
  unchanged: number
}

export interface HealthResponse {
  status: string
  docker: string
  database: string
  version: string
  uptime_seconds: number
}
