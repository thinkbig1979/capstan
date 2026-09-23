/**
 * The frontend's type surface.
 *
 * Wire shapes are NOT declared here. They are generated from the Go structs
 * that serve them — see backend/tygo.yaml — and re-exported below, so a
 * backend field that changes without its TypeScript is a failing required
 * check rather than a runtime surprise in the browser. Do not hand-edit
 * ./generated.ts or ./generated-truth.ts; regenerate them.
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

/* ------------------------------------------------------------------ *
 * Generated wire types, re-exported unchanged.
 * ------------------------------------------------------------------ */

export type {
  ActionLog,
  AppError,
  BackupSnapshot,
  CachedUpdate,
  ContainerMetrics,
  ContainerUpdateInfo,
  DiffResult,
  Directory,
  DockerImage,
  DockerNetwork,
  DockerVolume,
  GitCommit,
  GitStatusResult,
  LogResult,
  Outcome,
  PortBinding,
  Session,
  StackEvent,
  UpdateResult,
  User,
  WireActionResult,
}

/* ------------------------------------------------------------------ *
 * Generated wire types, re-narrowed where Go cannot express the union.
 * ------------------------------------------------------------------ */

type ContainerState = 'created' | 'running' | 'paused' | 'restarting' | 'removing' | 'exited' | 'dead'

export type StackStatus = 'running' | 'stopped' | 'partial' | 'unknown' | 'error'

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
  status: 'running' | 'success' | 'partial' | 'failed' | 'interrupted'
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

interface DiskUsageBreakdown {
  images: number
  containers: number
  volumes: number
  buildCache: number
  total: number
}

export interface AuthResponse {
  token: string
  user: User
}

export interface ConfiguredDir {
  path: string
  name: string
  isDefault: boolean
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

export interface GitRepoStatus {
  isRepo: true
  hasCommits: true
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

export type GitStatus = GitRepoStatus | GitEmptyRepoStatus | GitNotRepoStatus

/**
 * One line of a stack's env file. Hand-written counterpart of Go's
 * handlers.EnvEntry (backend/internal/handlers/env.go) — handlers is not a
 * tygo package, so the two declarations are kept in agreement by hand and by
 * backend/internal/handlers/env_wire_contract_test.go.
 *
 * `sensitive` is required and the Go tag carries no omitempty, so the server
 * always sends it (agent-os-6wrb). Before that fix the key was omitted on
 * false and every consumer here was correct only because `undefined` is
 * falsy.
 *
 * `line` is optional while Go always sends it. That is a ROLE difference,
 * not an oversight: this type is both the response shape, where `line` is
 * always present and 1-based, and the request/draft shape, where a row added
 * via "Add Entry" legitimately has none until the server parses the saved
 * file (env-editor/types.ts, EnvEntryRow). Optional is therefore the correct
 * declaration for the union of both roles. Requiring it fails `npx tsc -b`
 * at env-editor/useEnvEntryActions.ts (OBSERVED 2026-09-20, agent-os-6wrb).
 */
export interface EnvEntry {
  key: string
  value: string
  line?: number
  sensitive: boolean
  comment?: boolean
}

export interface EnvFilePresent {
  hasEnvFile: true
  filename: string
  entries: EnvEntry[]
  raw?: string
  locked?: boolean
}

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

export interface VersionInfo {
  version: string
  commit: string
  buildDate: string
}

export interface DockerCleanupPolicy {
  enabled: boolean
  minAgeHours: number
  intervalHours: number
  minAllowedAgeHours: number
  minAllowedIntervalHours: number
}

export interface DockerCleanupCandidate {
  id: string
  repository?: string
  size: number
  /** Unix seconds. */
  created: number
}

export interface DockerCleanupPreview {
  candidates: DockerCleanupCandidate[]
  reclaimableBytes: number
  minAgeHours: number
}

export interface DockerCleanupHistory {
  runs: DockerCleanupRun[]
  limit: number
}

