export interface User {
  id: string
  username: string
  createdAt?: string
}

export interface AuthResponse {
  token: string
  user: User
}

export type ContainerState = 'created' | 'running' | 'paused' | 'restarting' | 'removing' | 'exited' | 'dead'

export type StackTab = 'overview' | 'compose' | 'env' | 'git' | 'logs' | 'terminal'

export interface ConfiguredDir {
  path: string
  name: string
  isDefault: boolean
  stackCount?: number
  isGitRepo?: boolean
  gitBranch?: string
  gitBehind?: number
  gitAuthType?: string
  gitSshKeyPath?: string
  gitHttpsUser?: string
  hasHttpsToken?: boolean
  scannedAt?: string
}

export interface Directory {
  path: string
  name: string
  rootDir?: string
  stackCount: number
  isGitRepo: boolean
  gitBranch?: string
  gitRemote?: string
  gitAuthType?: string
  gitSshKeyPath?: string
  gitHttpsUser?: string
  hasHttpsToken?: boolean
  scannedAt: string
  gitAhead?: number
  gitBehind?: number
}

export type StackStatus = 'running' | 'stopped' | 'partial' | 'unknown' | 'error'

export interface Container {
  id: string
  name: string
  image: string
  state: ContainerState
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
  memSwap: number
  pids: number
}

export interface DashboardContainerInfo {
  id: string
  name: string
  image: string
  state: ContainerState
  status: string
  health: string
  ports: PortBinding[]
  stackId: string
  projectName: string
  restartCount: number
  created: string
  startedAt: string
  diskSize: number
  imageSize: number
}

export interface DiskUsageBreakdown {
  images: number
  containers: number
  volumes: number
  buildCache: number
  total: number
}

export interface DashboardStats {
  totalStacks: number
  runningStacks: number
  stoppedStacks: number
  totalContainers: number
  runningContainers: number
  imageDiskUsage: number
  diskUsage: DiskUsageBreakdown
  containers: DashboardContainerInfo[]
}

export interface Stack {
  id: string
  directory: string
  composeFile: string
  envFile?: string
  projectName: string
  status: StackStatus
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
  dirtyCount: number
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
  duration: number
}

export interface ApiError {
  error: string
  code: string
  details?: Record<string, unknown>
}

export interface ScanResult {
  directories: Directory[]
  scannedAt: string
  hasGlobalEnv: boolean
}

export interface DockerImage {
  id: string
  repoTags: string[]
  size: number
  created: number
  containers: number
}

export interface DockerVolume {
  name: string
  driver: string
  mountpoint: string
  size: number
  sizeKnown: boolean
  inUse: boolean
  created: string
  stack: string
}

export interface DockerNetwork {
  id: string
  name: string
  driver: string
  scope: string
  internal: boolean
  containers: number
  labels: string[]
  created: string
  stack: string
}

export interface BuildCacheEntry {
  ID: string
  Type: string
  Description: string
  InUse: boolean
  Shared: boolean
  Size: number
  CreatedAt: string
  LastUsedAt: string | null
  UsageCount: number
  Parents?: string[]
}

export interface HealthResponse {
  status: string
  docker: string
  database: string
  version: string
  uptime_seconds: number
}

export interface ContainerUpdateInfo {
  containerId: string
  containerName: string
  image: string
  imageRef: string
  state: string
  stackId: string
  projectName: string
  serviceName: string
  isCompose: boolean
}

export interface CachedUpdate {
  id: string
  containerId: string
  containerName: string
  image: string
  imageRef: string
  state: string
  stackId?: string
  projectName?: string
  serviceName?: string
  isCompose: boolean
  localDigest: string
  remoteDigest: string
  scannedAt: string
}

export interface UpdateHistoryEntry {
  id: string
  containerId: string
  containerName: string
  stackId?: string
  stackName?: string
  image: string
  oldDigest?: string
  newDigest?: string
  oldImageRef?: string
  newImageRef?: string
  status: 'pending' | 'success' | 'failed' | 'paused'
  trigger: 'manual' | 'auto'
  startedAt: string
  completedAt?: string
  durationMs?: number
  errorMessage?: string
}

export interface AutoUpdatePolicy {
  id: string
  targetType: 'container' | 'stack'
  targetId: string
  enabled: boolean
  consecutiveFailures: number
  paused: boolean
  createdAt: string
  updatedAt: string
}

/** How long each history table is kept, in days. `minRetentionDays` is the
 *  server-enforced floor — a prune is irreversible, so the API rejects less. */
export interface RetentionSettings {
  retentionDays: number
  updateHistoryRetentionDays: number
  backupHistoryRetentionDays: number
  minRetentionDays: number
}

export interface UpdateSettings {
  scanIntervalMinutes: number
  lastScanAt: string | null
  lastScanError: string | null
  globalAutoUpdate: boolean
  autoUpdateStats: {
    enabledContainers: number
    updatesLast7Days: number
    updatesLast30Days: number
  }
}

export interface UpdateHistoryFilters {
  page?: number
  limit?: number
  status?: string
  trigger?: string
  containerId?: string
  stackId?: string
  from?: string
  to?: string
}

// ────────────────────────────────────────────────
// Backup types
// ────────────────────────────────────────────────

export interface BackupPolicy {
  id: string
  targetType: 'stack'
  targetId: string
  enabled: boolean
  stopPolicy: 'stop' | 'hot'
  createdAt: string
  updatedAt: string
}

export interface BackupRun {
  id: string
  kind: 'backup' | 'sync' | 'restore' | 'dr_restore' | 'prune'
  trigger: 'manual' | 'scheduled'
  status: 'running' | 'success' | 'partial' | 'failed'
  startedAt: string
  finishedAt?: string | null
  stacksTotal: number
  stacksOk: number
  stacksFailed: number
  bytesAdded?: number | null
  errorMessage?: string
}

export interface BackupRunItem {
  id: string
  runId: string
  stackId: string
  status: 'skipped' | 'success' | 'failed'
  snapshotId?: string
  stopApplied: boolean
  durationMs: number
  errorMessage?: string
}

export interface BackupSnapshot {
  id: string
  shortId: string
  time: string
  hostname: string
  tags: string[]
  paths: string[]
  sizeBytes?: number
}

export interface BackupSettings {
  repository: string
  repositorySource: 'env' | 'db' | 'default'
  hasPassword: boolean
  passwordSource: 'env' | 'db' | 'default'
  keepDaily: number
  keepWeekly: number
  keepMonthly: number
  keepYearly: number
  autoPrune: boolean
  scheduleIntervalMinutes: number
  syncAfterBackup: boolean
  rcloneRemote: string
  rclonePath: string
  rcloneTransfers: number
  hostname: string
  resticAvailable: boolean
  rcloneAvailable: boolean
  repositoryInitialized: boolean
}

export interface BackupStatus {
  resticAvailable: boolean
  rcloneAvailable: boolean
  repositoryInitialized: boolean
  enabledStackCount: number
  lastRun: BackupRun | null
  nextRunAt: string | null
  repoSizeBytes: number | null
  schedulerRunning: boolean
}

/** Response shape for operation endpoints that stream output over WS */
export interface BackupOperationResult {
  runId: string
  wsUrl: string
}

/** Build identity of the running backend, from GET /api/v1/version. */
export interface VersionInfo {
  version: string
  commit: string
  buildDate: string
}
