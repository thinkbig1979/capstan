export interface User {
  id: string
  username: string
  createdAt?: string
}

export interface AuthResponse {
  token: string
  user: User
}

type ContainerState = 'created' | 'running' | 'paused' | 'restarting' | 'removing' | 'exited' | 'dead'

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

// DirectoryCredentialStatusValue mirrors the fixed enum returned by
// GET /directories/credential-status (backend/internal/handlers/directories.go).
// It is deliberately its own type, not a field on ConfiguredDir: the probe
// decrypts the stored token to determine it, which directoriesApi.list()
// (backed by ListDirectories) never does and must not start doing — see the
// comment on ListDirectories in backend/internal/database/directories.go.
export type DirectoryCredentialStatusValue = 'none' | 'ok' | 'unreadable' | 'empty'

export interface DirectoryCredentialStatus {
  path: string
  status: DirectoryCredentialStatusValue
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

interface PortBinding {
  host: string
  container: string
  protocol: string
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

interface DiskUsageBreakdown {
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

/**
 * The shape of `error.response.data` — the raw HTTP response BODY the
 * backend serializes for an AppError (models/errors.go:44-49): `code`/
 * `message`/`details` only. There is no `error` field on this body; the
 * backend never sends one, so a consumer reading `.error` off it was
 * permanently dead code (agent-os-m2x). This is the ONLY thing this type
 * models — see api.ts:87,94.
 *
 * It does NOT model the value api.ts's response interceptor ultimately
 * rejects with, which is a different, untyped object:
 *   - on an HTTP error response: this body flattened with `status` injected
 *     (api.ts:117) — still no `.error`.
 *   - on a network/timeout failure with no HTTP response at all: a
 *     synthesized `{ error: 'Unknown error', code, message }` (api.ts:118),
 *     where `.error` IS real. error-handler.ts's `data?.error` arm exists
 *     for that path (and for the older bare `gin.H{"error":...}` handlers),
 *     not for this type.
 */
export interface ApiError {
  code: string
  message: string
  details?: Record<string, unknown>
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

/**
 * GET /resources/build-cache. Mirrors handlers.BuildCacheEntry — the backend
 * declares its own response type rather than serializing the Docker SDK struct,
 * so these are lowerCamelCase like the rest of the API. `parents` is omitted
 * when empty (agent-os-iuby).
 */
export interface BuildCacheEntry {
  id: string
  type: string
  description: string
  inUse: boolean
  shared: boolean
  size: number
  createdAt: string
  lastUsedAt: string | null
  usageCount: number
  parents?: string[]
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
  /** Whether detected updates apply as soon as they are found, or on a schedule. */
  applyMode: 'immediate' | 'scheduled'
  /** "HH:MM" in server local time. */
  applyTime: string
  /** Go weekday ints, 0 = Sunday. Never null. */
  applyDays: number[]
  /** RFC3339. Omitted while no scheduled apply is pending. */
  nextApplyAt?: string
  /** The server's own zone, reported for display. There is no timezone setting. */
  serverTimezone: string
  serverTimeOffset: string
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
  // 'interrupted' (agent-os-pid): a run left 'running' by a crash or a
  // restore from a mid-run snapshot. Distinct from 'failed' -- it never
  // reported a real outcome and may have succeeded on the original instance.
  status: 'running' | 'success' | 'partial' | 'failed' | 'interrupted'
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
  /** Whether backups run on a fixed interval or at a time of day. */
  scheduleMode: 'interval' | 'scheduled'
  /** "HH:MM" in server local time. */
  scheduleTime: string
  /** Go weekday ints, 0 = Sunday. Never null. */
  scheduleDays: number[]
  /** The server's own zone, reported for display. There is no timezone setting. */
  serverTimezone: string
  serverTimeOffset: string
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
