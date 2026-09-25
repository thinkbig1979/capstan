/**
 * The frontend's type surface.
 *
 * Wire shapes are NOT declared here. They are generated from the Go structs
 * that serve them — see backend/tygo.yaml — and re-exported below, so a
 * backend field that changes without its TypeScript is a failing required
 * check rather than a runtime surprise in the browser. Do not hand-edit
 * any ./generated*.ts file; regenerate them.
 *
 * Three things still live here by hand, each for a reason that is not going
 * away:
 *
 *  1. LITERAL UNIONS. Go has no enum type, so a field the backend constrains
 *     to a fixed set of strings generates as a bare `string`. Those fields are
 *     re-narrowed below with `Omit<Wire, 'f'> & { f: Union }`. The generated
 *     interface still owns the FIELD SET and every other field's type, so an
 *     added or renamed Go field still flows through and still trips the gate;
 *     only the union is hand-maintained. Each one says which Go field it
 *     narrows. Do not "simplify" them away — nothing generates them.
 *
 *  2. SHAPES WITH NO SINGLE GO STRUCT BEHIND THEM. Responses composed from a
 *     `gin.H` literal, discriminated unions with a raw-map arm, and shapes
 *     that exist only in the browser.
 *
 *  3. REQUEST shapes. tygo generates responses; query and body types are the
 *     caller's contract, not the server's.
 */

import type {
  ActionResult as WireActionResult,
  Outcome,
} from './generated-truth'
import type {
  ActionLog,
  AppError,
  AutoUpdatePolicy as WireAutoUpdatePolicy,
  BackupPolicy as WireBackupPolicy,
  BackupRun as WireBackupRun,
  BackupRunItem as WireBackupRunItem,
  BackupSnapshot,
  CachedUpdate,
  Container as WireContainer,
  ContainerMetrics,
  ContainerUpdateInfo,
  DashboardContainerInfo as WireDashboardContainerInfo,
  DiffResult,
  Directory,
  DockerCleanupRun as WireDockerCleanupRun,
  DockerImage,
  DockerNetwork,
  DockerVolume,
  GitCommit,
  GitStatusResult,
  LintResult as WireLintResult,
  LogResult,
  PortBinding,
  Session,
  Stack as WireStack,
  StackEvent,
  UpdateHistoryEntry as WireUpdateHistoryEntry,
  UpdateResult,
  UpdateSettingsResponse,
  User,
} from './generated'
import type {
  BuildCacheEntry,
  ComposeResponse,
  ComposeSaveResponse as WireComposeSaveResponse,
  EnvEntry,
  EnvResponse,
  LintResponse as WireLintResponse,
} from './generated-handlers'
import type {
  DiskUsageBreakdown,
  DockerCleanupCandidate,
  DockerCleanupPreview,
} from './generated-services'
import type { Info as VersionInfo } from './generated-version'

/* ------------------------------------------------------------------ *
 * Generated wire types, re-exported unchanged.
 * ------------------------------------------------------------------ */

export type {
  ActionLog,
  AppError,
  BackupSnapshot,
  BuildCacheEntry,
  CachedUpdate,
  ComposeResponse,
  ContainerMetrics,
  ContainerUpdateInfo,
  DiffResult,
  Directory,
  DiskUsageBreakdown,
  DockerCleanupCandidate,
  DockerCleanupPreview,
  DockerImage,
  DockerNetwork,
  DockerVolume,
  EnvEntry,
  GitCommit,
  GitStatusResult,
  LogResult,
  Outcome,
  PortBinding,
  Session,
  StackEvent,
  UpdateResult,
  User,
  VersionInfo,
  WireActionResult,
}

/* ------------------------------------------------------------------ *
 * Generated wire types, re-narrowed where Go cannot express the union.
 * ------------------------------------------------------------------ */

type ContainerState = 'created' | 'running' | 'paused' | 'restarting' | 'removing' | 'exited' | 'dead'

// 'paused' is never computed by the stacks API; it arrives on /ws/events as a
// stack_status frame for a Docker pause (MonitorService.stackEventFor).
export type StackStatus = 'running' | 'stopped' | 'partial' | 'paused' | 'unknown' | 'error'

/**
 * Re-points a generated array field at this file's narrowed element type while
 * keeping whatever nullability the GENERATED field declares.
 *
 * Omit is not enough on its own for a field whose element type is itself
 * narrowed here: `Omit<WireStack, 'status'>` leaves `containers` typed as the
 * GENERATED Container (`state: string`), which is not assignable to this
 * file's Container (`state: ContainerState`), and every consumer passing a
 * stack's containers to a Container-typed parameter fails.
 *
 * Extract<Wire, null> is how the nullability stays generated rather than
 * hand-copied: it is `null` when the Go field carries tstype:"T[] | null" and
 * `never` — which unions away to nothing — when it does not. Drop the Go tag
 * and this type follows, with no edit here.
 */
type NarrowedArray<Wire, Element> = Element[] | Extract<Wire, null>

// narrows models.Stack.Status, a Go string, and re-points Containers at the
// narrowed Container above
export type Stack = Omit<WireStack, 'status' | 'containers'> & {
  status: StackStatus
  containers: NarrowedArray<WireStack['containers'], Container>
}

// narrows models.Container.State, a Go string
export type Container = Omit<WireContainer, 'state'> & { state: ContainerState }

// narrows models.DashboardContainerInfo.State, a Go string
export type DashboardContainerInfo = Omit<WireDashboardContainerInfo, 'state'> & {
  state: ContainerState
}

// narrows models.LintResult.Level, a Go string
export type LintResult = Omit<WireLintResult, 'level'> & {
  level: 'error' | 'warning' | 'info'
}

// handlers.ComposeSaveResponse and handlers.LintResponse, with lintResults
// re-pointed at the narrowed LintResult above
export type ComposeSaveResponse = Omit<WireComposeSaveResponse, 'lintResults'> & {
  lintResults?: NarrowedArray<WireComposeSaveResponse['lintResults'], LintResult>
}
export type LintResponse = Omit<WireLintResponse, 'lintResults'> & {
  lintResults: NarrowedArray<WireLintResponse['lintResults'], LintResult>
}

// narrows models.AutoUpdatePolicy.TargetType, a Go string
export type AutoUpdatePolicy = Omit<WireAutoUpdatePolicy, 'targetType'> & {
  targetType: 'container' | 'stack'
}

// narrows models.BackupPolicy.TargetType and .StopPolicy, both Go strings
export type BackupPolicy = Omit<WireBackupPolicy, 'targetType' | 'stopPolicy'> & {
  targetType: 'stack'
  stopPolicy: 'stop' | 'hot'
}

// narrows models.BackupRun.Kind, .Trigger and .Status, all Go strings.
// FinishedAt and BytesAdded are Go pointers WITH omitempty, so a nil omits the
// key entirely — the generated `?:` is right and the `| null` this type used to
// carry described a wire that cannot occur.
export type BackupRun = Omit<WireBackupRun, 'kind' | 'trigger' | 'status'> & {
  kind: 'backup' | 'sync' | 'restore' | 'dr_restore' | 'prune' | 'verify'
  trigger: 'manual' | 'scheduled'
  // 'skipped' (agent-os-4i7r): a scheduled backup that never started.
  status: 'running' | 'success' | 'partial' | 'failed' | 'interrupted' | 'skipped'
}

// narrows models.BackupRunItem.Status, a Go string
export type BackupRunItem = Omit<WireBackupRunItem, 'status'> & {
  status: 'skipped' | 'success' | 'failed'
}

// narrows models.UpdateHistoryEntry.Status and .Trigger, both Go strings
export type UpdateHistoryEntry = Omit<WireUpdateHistoryEntry, 'status' | 'trigger'> & {
  status: 'pending' | 'success' | 'failed' | 'paused'
  trigger: 'manual' | 'auto'
}

// narrows models.DockerCleanupRun.Trigger and .Status, both Go strings
export type DockerCleanupRun = Omit<WireDockerCleanupRun, 'trigger' | 'status'> & {
  trigger: 'scheduled' | 'manual'
  status: 'success' | 'failed'
}

// narrows models.UpdateSettingsResponse.ApplyMode, a Go string.
// LastScanAt and LastScanError are Go strings WITH omitempty: an empty value
// omits the key, so they arrive absent and never as null.
export type UpdateSettings = Omit<UpdateSettingsResponse, 'applyMode'> & {
  applyMode: 'immediate' | 'scheduled'
}

/* ------------------------------------------------------------------ *
 * Shapes with no single Go struct behind them, and request types.
 * ------------------------------------------------------------------ */

export interface AuthResponse {
  token: string
  user: User
}

export interface ConfiguredDir {
  path: string
  name: string
  // Optional: GET /directories and POST /directories/scan serialise
  // models.Directory, which has no such field (agent-os-zfa5).
  isDefault?: boolean
  stackCount?: number
  isGitRepo?: boolean
  gitBranch?: string
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

// agent-os-p9e1: every key but totalStacks is omitted when the Docker read
// behind it failed (backend/internal/handlers/dashboard.go getDashboardStats).
export interface DashboardStats {
  totalStacks: number
  runningStacks?: number
  stoppedStacks?: number
  totalContainers?: number
  runningContainers?: number
  imageDiskUsage?: number
  diskUsage?: DiskUsageBreakdown
  containers?: DashboardContainerInfo[]
}

export interface GitRepoStatus {
  isRepo: true
  hasCommits: true
  isBare: false
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

export interface GitNotRepoStatus {
  isRepo: false
}

export interface GitEmptyRepoStatus {
  isRepo: true
  hasCommits: false
}

// agent-os-m2g8: a repository with no work tree. The work-tree fields are
// absent on the wire, not zero, so they are absent here and reading one is a
// compile error rather than a rendered "clean".
export interface GitBareRepoStatus {
  isRepo: true
  hasCommits: true
  isBare: true
  branch: string
  commit: string
  commitShort: string
  commitMessage: string
  commitAuthor: string
  commitDate: string
  remote: string
}

export type GitStatus = GitRepoStatus | GitBareRepoStatus | GitEmptyRepoStatus | GitNotRepoStatus

// narrows handlers.EnvResponse.HasEnvFile, a Go bool, to the literal the
// present branch always carries. Entries stays the generated EnvEntry, whose
// `line` is required: every row on this path comes from parseEnvFile and is
// 1-based.
export type EnvFilePresent = Omit<EnvResponse, 'hasEnvFile'> & { hasEnvFile: true }

/**
 * A REQUEST row for PUT /:id/env, and the editor's draft row
 * (env-editor/types.ts, EnvEntryRow). Hand-written because it is the caller's
 * contract, not the server's: a row added via "Add Entry" has no line until
 * the server parses the saved file. An absent `line` decodes to 0 in Go, and
 * no server code reads EnvEntry.Line from a request. Derived from the generated EnvEntry so
 * the field set still follows Go; only `line` is loosened, and only here, so
 * the response shape above keeps stating that `line` is always present
 * (agent-os-tuxp).
 */
export type EnvEntryDraft = Omit<EnvEntry, 'line'> & { line?: number }

export interface EnvFileAbsent {
  hasEnvFile: false
}

export type EnvFileResponse = EnvFilePresent | EnvFileAbsent

export interface CommandResult {
  status: string
  output: string
  duration: number
}

export interface ApiError {
  code: string
  message: string
  details?: Record<string, unknown>
}

export interface RetentionSettings {
  retentionDays: number
  updateHistoryRetentionDays: number
  backupHistoryRetentionDays: number
  minRetentionDays: number
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

export interface BackupHistoryFilters {
  page: number
  limit: number
  status?: string
  kind?: string
  trigger?: string
  from?: string
  to?: string
}

export interface BackupHistoryResponse {
  runs: BackupRun[]
  total: number
  page: number
  limit: number
  totalPages: number
}

export interface BackupSettings {
  repository: string
  /**
   * True when `repository` above had an embedded credential stripped out of it
   * and replaced with `***`. Optional because a server predating the field
   * omits it; absent reads the same as false, which is the safe default — it
   * warns about nothing rather than warning about everything.
   */
  hasEmbeddedCredential?: boolean
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
  /**
   * Which of the repository's mutually exclusive states the last probe found,
   * mirroring the Go constants in backend/internal/services/backup.go. `''` is
   * "not probed" and is genuinely on the wire: both handlers build their
   * response as a `gin.H` map, so the Go `omitempty` never applies and the key
   * ships empty when restic is absent. The states are NOT interchangeable --
   * `uninitialized` calls for creating a repository and `unreachable` must not,
   * since a repository that merely went unreadable may hold every backup the
   * user has.
   */
  repoState:
    | ''
    | 'ok'
    | 'uninitialized'
    | 'unreachable'
    | 'settings_unreadable'
    | 'password_missing'
    | 'wrong_password'
  /** Human-readable cause behind a non-`ok` `repoState`. Empty when there is none. */
  repoStateMessage: string
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
  /**
   * Which of the repository's mutually exclusive states the last probe found,
   * mirroring the Go constants in backend/internal/services/backup.go. `''` is
   * "not probed" and is genuinely on the wire: both handlers build their
   * response as a `gin.H` map, so the Go `omitempty` never applies and the key
   * ships empty when restic is absent. The states are NOT interchangeable --
   * `uninitialized` calls for creating a repository and `unreachable` must not,
   * since a repository that merely went unreadable may hold every backup the
   * user has.
   */
  repoState:
    | ''
    | 'ok'
    | 'uninitialized'
    | 'unreachable'
    | 'settings_unreadable'
    | 'password_missing'
    | 'wrong_password'
  /** Human-readable cause behind a non-`ok` `repoState`. Empty when there is none. */
  repoStateMessage: string
  enabledStackCount: number
  lastRun: BackupRun | null
  /**
   * The newest `verify` run, reported SEPARATELY from `lastRun` on purpose
   * (agent-os-j1jw): `lastRun` is the newest run of ANY kind, so the next
   * backup that succeeds would hide a failed verification behind it. A failed
   * verification does not block backups, which makes this the only channel by
   * which an operator learns the repository may not be restorable. Required,
   * not optional: the handler always ships the key and sends null when no
   * verify has ever run (TestGetStatus_LastVerifyNullWhenNeverRun).
   */
  lastVerify: BackupRun | null
  nextRunAt: string | null
  repoSizeBytes: number | null
  schedulerRunning: boolean
}

export interface BackupOperationResult {
  runId: string
  wsUrl: string
}

export interface DockerCleanupPolicy {
  enabled: boolean
  minAgeHours: number
  intervalHours: number
  minAllowedAgeHours: number
  minAllowedIntervalHours: number
}

export interface DockerCleanupHistory {
  runs: DockerCleanupRun[]
  limit: number
}

