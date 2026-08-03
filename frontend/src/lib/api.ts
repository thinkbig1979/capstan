import axios, { AxiosError, type AxiosInstance } from 'axios'
import type { ActionResult } from '@/lib/action-result'
import type {
  User,
  AuthResponse,
  ConfiguredDir,
  DirectoryCredentialStatus,
  Stack,
  CommandResult,
  ApiError,
  LintResult,
  DashboardStats,
  DockerImage,
  DockerVolume,
  DockerNetwork,
  BuildCacheEntry,
  ContainerUpdateInfo,
  CachedUpdate,
  UpdateHistoryEntry,
  AutoUpdatePolicy,
  UpdateSettings,
  RetentionSettings,
  UpdateHistoryFilters,
  GitStatus,
  GitCommit,
  BackupPolicy,
  BackupRun,
  BackupRunItem,
  BackupSnapshot,
  BackupSettings,
  BackupStatus,
  BackupOperationResult,
  VersionInfo,
} from '@/types'

/**
 * LifecycleResult is the wire type for stack start/stop/restart/pull responses.
 *
 * Today the backend returns {status, output, duration} (CommandResult shape).
 * Once the backend is migrated to the Action Truth Contract it will return
 * {outcome, reason, details?} (ActionResult shape) with HTTP 200/207/500.
 *
 * Both shapes are surfaced here so callers can use isActionResult() to branch
 * during the migration window.
 */
export type LifecycleResult = ActionResult & Pick<CommandResult, 'status' | 'output' | 'duration'>

const API_BASE_URL = '/api/v1'

function getCSRFToken(): string | null {
  const match = document.cookie.match(/(?:^|;\s*)capstan_csrf=([^;]*)/)
  return match ? decodeURIComponent(match[1]) : null
}

const apiClient: AxiosInstance = axios.create({
  baseURL: API_BASE_URL,
  // Match backend stacksGroup timeout (120s in main.go) so long-running
  // operations like pull/start don't abort client-side while the backend
  // is still working. Streaming operations go through WebSockets, not this client.
  timeout: 120000,
  headers: {
    'Content-Type': 'application/json',
  },
  withCredentials: true,
})

let getToken: (() => string | null) | null = null
let logout: (() => void) | null = null

apiClient.interceptors.request.use((config) => {
  const token = getToken?.()
  if (token && token !== 'cookie') {
    config.headers.Authorization = `Bearer ${token}`
  }

  const csrfToken = getCSRFToken()
  if (csrfToken) {
    config.headers['X-CSRF-Token'] = csrfToken
  }

  return config
})

apiClient.interceptors.response.use(
  (response) => response,
  (error: AxiosError<ApiError>) => {
    if (error.response?.status === 401 && logout) {
      const url = error.config?.url
      // /auth/me is the boot-time session probe; a 401 just means "not
      // logged in" and is handled by the store, not a session-expiry signal.
      // /auth/status and /auth/login also routinely 401 without meaning
      // the user was logged out mid-session.
      const isAuthEndpoint =
        url?.includes('/auth/status') ||
        url?.includes('/auth/login') ||
        url?.includes('/auth/me')
      if (!isAuthEndpoint) {
        logout()
      }
    }
    // The interceptor used to reject with the bare response body, which has
    // no `status` field — every status-specific branch in classifyError()
    // was dead code (agent-os-yj0). Inject the status onto a fresh object
    // (never mutate error.response.data) so classifyError can read it.
    // When there's no response at all (network/timeout failure), preserve
    // axios's own `code`/`message` instead of discarding them — they're the
    // only signal classifyError has for the network/timeout branches.
    const safeError = error.response
      ? { ...error.response.data, status: error.response.status }
      : { error: 'Unknown error', code: error.code || 'UNKNOWN', message: error.message }
    return Promise.reject(safeError)
  },
)

export function setAuthCallbacks(getTokenFn: () => string | null, logoutFn: () => void) {
  getToken = getTokenFn
  logout = logoutFn
}

export const authApi = {
  setup: async (username: string, password: string) => {
    const response = await apiClient.post<AuthResponse>('/auth/setup', { username, password })
    return response.data
  },

  login: async (username: string, password: string) => {
    const response = await apiClient.post<AuthResponse>('/auth/login', { username, password })
    return response.data
  },

  logout: async () => {
    const response = await apiClient.post<void>('/auth/logout')
    return response.data
  },

  me: async () => {
    const response = await apiClient.get<User>('/auth/me')
    return response.data
  },

  status: async () => {
    const response = await apiClient.get<{ needsSetup: boolean; authDisabled: boolean }>('/auth/status')
    return response.data
  },

  changePassword: async (currentPassword: string, newPassword: string) => {
    const response = await apiClient.put<void>('/auth/password', {
      currentPassword,
      newPassword,
    })
    return response.data
  },

  verifyPassword: async (password: string) => {
    const response = await apiClient.post<{ ok: boolean }>('/auth/verify-password', { password })
    return response.data
  },
}

/** Build identity of the running backend. Public endpoint — no session needed. */
export const versionApi = {
  get: async () => {
    const response = await apiClient.get<VersionInfo>('/version')
    return response.data
  },
}

export const settingsApi = {
  getGlobalEnv: async () => {
    const response = await apiClient.get<{ vars: Array<{ key: string; value: string }> }>('/settings/global-env')
    return response.data
  },

  getConfig: async () => {
    const response = await apiClient.get<{ stacksDir: string; stacksDirectories: string[] }>('/settings/config')
    return response.data
  },

  updateGlobalEnv: async (vars: Array<{ key: string; value: string }>) => {
    const response = await apiClient.put<void>('/settings/global-env', { vars })
    return response.data
  },

  getUpdates: async () => {
    const response = await apiClient.get<UpdateSettings>('/settings/updates')
    return response.data
  },

  updateUpdates: async (data: { scanIntervalMinutes: number; globalAutoUpdate: boolean }) => {
    const response = await apiClient.put<UpdateSettings>('/settings/updates', data)
    return response.data
  },

  getGit: async () => {
    const response = await apiClient.get<{ sshKey: string; httpsUser: string; hasHttpsToken: boolean }>('/settings/git')
    return response.data
  },

  updateGit: async (data: { sshKey?: string; httpsUser?: string; httpsToken?: string }) => {
    const response = await apiClient.put<{ sshKey: string; httpsUser: string; hasHttpsToken: boolean }>('/settings/git', data)
    return response.data
  },

  getRetention: async () => {
    const response = await apiClient.get<RetentionSettings>('/settings/log-retention')
    return response.data
  },

  updateRetention: async (data: Partial<Omit<RetentionSettings, 'minRetentionDays'>>) => {
    await apiClient.put('/settings/log-retention', data)
  },

  getAuditLog: async (
    page = 1,
    pageSize = 50,
    filters: { action?: string; search?: string; dateFrom?: string; dateTo?: string } = {},
  ) => {
    const params: Record<string, string | number> = { page, pageSize }
    if (filters.action) params.action = filters.action
    if (filters.search) params.search = filters.search
    if (filters.dateFrom) params.dateFrom = filters.dateFrom
    if (filters.dateTo) params.dateTo = filters.dateTo
    const response = await apiClient.get<{
      entries: Array<{ id: string; userId: string; stackId: string; action: string; detail: string; createdAt: string }>
      total: number
      page: number
      pageSize: number
      availableActions: string[]
    }>('/settings/audit-log', { params })
    return response.data
  },

  getScanDepth: async () => {
    const response = await apiClient.get<{ scanDepth: number }>('/settings/scan-depth')
    return response.data
  },

  updateScanDepth: async (scanDepth: number) => {
    const response = await apiClient.put<{ scanDepth: number }>('/settings/scan-depth', { scanDepth })
    return response.data
  },
}

export const autoUpdateApi = {
  getPolicies: async () => {
    const response = await apiClient.get<{
      globalEnabled: boolean
      policies: AutoUpdatePolicy[]
    }>('/resources/auto-update/policies')
    return response.data
  },

  setPolicy: async (targetType: string, targetId: string, data: { enabled: boolean }) => {
    const response = await apiClient.put<AutoUpdatePolicy>(
      `/resources/auto-update/policies/${targetType}/${encodeURIComponent(targetId)}`,
      data,
    )
    return response.data
  },

  deletePolicy: async (targetType: string, targetId: string) => {
    await apiClient.delete(`/resources/auto-update/policies/${targetType}/${encodeURIComponent(targetId)}`)
  },
}

/**
 * GitPullResult — wire type for POST /git/pull.
 *
 * Legacy shape (current backend):
 *   { success: boolean, previousCommit, currentCommit, changedFiles, redeployedStacks }
 *
 * Action Truth Contract shape (post-B4 backend migration):
 *   ActionResult with outcome 'success'|'no_change'|'partial'|'failed'
 *   details: { previousCommit, currentCommit, failedRedeploys: [{stack, reason}] }
 *
 * Use isActionResult() to branch during the migration window.
 */
export type GitPullResult = ActionResult<{
  previousCommit?: string
  currentCommit?: string
  failedRedeploys?: Array<{ stack: string; reason: string }>
  changedFiles?: string[]
  redeployedStacks?: string[]
}> | {
  success: boolean
  previousCommit: string
  currentCommit: string
  changedFiles: string[]
  redeployedStacks: string[]
}

/**
 * EnvSaveResult — wire type for PUT /stacks/:id/env.
 *
 * Legacy shape: { saved: boolean, filename }
 * Action Truth Contract shape: ActionResult
 */
export type EnvSaveResult = ActionResult | { saved: boolean; filename: string }

/**
 * ComposeEnvResult — wire type for PUT /stacks/:id/compose-env (atomic).
 *
 * Introduced in B4. Writes compose + env in a single transaction.
 * Backend ComposeEnvRequest: { composeContent (required), envRaw?, envEntries? }
 * Returns ActionResult with details: { compose, env?, lintResults? }
 */
export type ComposeEnvResult = ActionResult<{
  compose?: string
  env?: string
  lintResults?: unknown[]
}>

export const gitApi = {
  status: async (stackId: string) => {
    const response = await apiClient.get<GitStatus>(`/git?stackId=${encodeURIComponent(stackId)}`)
    return response.data
  },

  pull: async (stackId: string, redeploy = false) => {
    const response = await apiClient.post<GitPullResult>(`/git/pull?stackId=${encodeURIComponent(stackId)}&redeploy=${redeploy}`)
    return response.data
  },

  log: async (stackId: string, limit = 50, offset = 0, file?: string) => {
    const response = await apiClient.get<{ commits: GitCommit[]; total: number; hasMore: boolean }>(`/git/log?stackId=${encodeURIComponent(stackId)}`, { params: { limit, offset, file } })
    return response.data
  },

  diff: async (stackId: string, hash: string) => {
    const response = await apiClient.get<{ commit: string; diff: string }>(`/git/diff/${encodeURIComponent(hash)}?stackId=${encodeURIComponent(stackId)}`)
    return response.data
  },
}

export const stacksApi = {
  list: async () => {
    const response = await apiClient.get<{ stacks: Stack[] }>('/stacks')
    return response.data.stacks
  },

  get: async (id: string) => {
    const response = await apiClient.get<Stack>(`/stacks/${encodeURIComponent(id)}`)
    return response.data
  },

  start: async (id: string) => {
    const response = await apiClient.post<LifecycleResult>(`/stacks/${encodeURIComponent(id)}/start`)
    return response.data
  },

  stop: async (id: string) => {
    const response = await apiClient.post<LifecycleResult>(`/stacks/${encodeURIComponent(id)}/stop`)
    return response.data
  },

  restart: async (id: string) => {
    const response = await apiClient.post<LifecycleResult>(`/stacks/${encodeURIComponent(id)}/restart`)
    return response.data
  },

  pull: async (id: string) => {
    const response = await apiClient.post<LifecycleResult>(`/stacks/${encodeURIComponent(id)}/pull`)
    return response.data
  },

  /**
   * The backend requires ?confirm=true (it 400s otherwise). The UI already
   * gates this behind its own confirmation dialog, so the intent is confirmed.
   *
   * `confirmCollateral` must be passed only after a SECOND, explicit user
   * confirmation of the specific files the backend reported it would also
   * destroy (a 428 STACK_DELETE_COLLATERAL response — see stack-delete.ts).
   * It must never be sent unconditionally: that would silently defeat the
   * backend's guard against destroying collateral files (agent-os-lg2).
   */
  delete: async (id: string, confirmCollateral = false) => {
    const collateralParam = confirmCollateral ? '&confirmCollateral=true' : ''
    const response = await apiClient.delete<StackDeleteResult>(
      `/stacks/${encodeURIComponent(id)}?confirm=true${collateralParam}`,
    )
    return response.data
  },

  getCompose: async (id: string) => {
    const response = await apiClient.get<{ content: string; filename: string; size: number; lastModified: string }>(`/stacks/${encodeURIComponent(id)}/compose`)
    return response.data
  },

  updateCompose: async (id: string, content: string) => {
    const response = await apiClient.put<void>(`/stacks/${encodeURIComponent(id)}/compose`, { content })
    return response.data
  },

  lintCompose: async (id: string, content: string) => {
    const response = await apiClient.post<{ valid: boolean; lintResults: LintResult[] }>(`/stacks/${encodeURIComponent(id)}/compose/lint`, { content })
    return response.data
  },

  getEnv: async (id: string) => {
    const response = await apiClient.get<{ filename: string; entries: Array<{ key: string; value: string; line: number; sensitive: boolean; comment: boolean }>; raw: string }>(`/stacks/${encodeURIComponent(id)}/env`)
    return response.data
  },

  updateEnv: async (id: string, body: { entries?: Array<{ key: string; value: string; sensitive?: boolean; comment?: boolean }>; raw?: string }) => {
    const response = await apiClient.put<EnvSaveResult>(`/stacks/${encodeURIComponent(id)}/env`, body)
    return response.data
  },

  /** Create a new .env file for a stack that doesn't have one yet. POST /stacks/:id/env */
  createEnv: async (id: string, content = '') => {
    const response = await apiClient.post<ActionResult<{ filename: string }>>(`/stacks/${encodeURIComponent(id)}/env`, { raw: content })
    return response.data
  },

  /**
   * Atomically write compose + env in one request.
   * PUT /stacks/:id/compose-env
   *
   * Wire body (matches backend ComposeEnvRequest):
   *   { composeContent: string (required), envRaw?: string, envEntries?: EnvEntry[] }
   *
   * Introduced in B4 to replace the two-request extract-to-env flow (#11).
   * If the backend hasn't migrated yet this will 404 — callers should
   * fall back to sequential writes.
   */
  updateComposeAndEnv: async (
    id: string,
    composeContent: string,
    envRaw: string,
  ) => {
    const response = await apiClient.put<ComposeEnvResult>(
      `/stacks/${encodeURIComponent(id)}/compose-env`,
      { composeContent, envRaw },
    )
    return response.data
  },

  create: async (input: { name: string; directory?: string; composeContent: string; envContent?: string; deploy: boolean }) => {
    const response = await apiClient.post<CreateStackResult>('/stacks', input)
    return response.data
  },

  lint: async (content: string) => {
    const response = await apiClient.post<{ valid: boolean; lintResults: LintResult[] }>('/compose/lint', { compose: content })
    return response.data
  },
}

export const directoriesApi = {
  list: async () => {
    const response = await apiClient.get<{ directories: ConfiguredDir[] }>('/directories')
    return response.data.directories
  },

  scan: async () => {
    const response = await apiClient.post<{ directories: ConfiguredDir[]; hasGlobalEnv: boolean; scannedAt: string }>('/directories/scan')
    return response.data
  },

  get: async (path: string) => {
    const response = await apiClient.get<ConfiguredDir>(`/directories/${encodeURIComponent(path)}`)
    return response.data
  },

  // The path travels in the JSON body, not the URL: the backend registers a
  // single static PUT /directories/credentials route (see directories.go),
  // and gin's decoded-path matching can't route an absolute, slash-containing
  // directory path through one URL segment. See agent-os-p7r.
  updateCredentials: async (directoryPath: string, credentials: { authType?: string; sshKeyPath?: string; httpsUser?: string; httpsToken?: string }) => {
    const response = await apiClient.put<void>('/directories/credentials', { path: directoryPath, ...credentials })
    return response.data
  },

  // Same reasoning as updateCredentials above: the path travels as a query
  // parameter, not a URL segment, so an absolute path with slashes routes
  // correctly. See agent-os-8a5.
  credentialStatus: async (directoryPath: string) => {
    const response = await apiClient.get<DirectoryCredentialStatus>('/directories/credential-status', {
      params: { path: directoryPath },
    })
    return response.data
  },
}

export const dashboardApi = {
  stats: async () => {
    const response = await apiClient.get<DashboardStats>('/dashboard/stats')
    return response.data
  },
}

export const directoryConfigApi = {
  get: async () => {
    const response = await apiClient.get<{ directories: string[]; defaultDir: string }>('/settings/directories')
    return response.data
  },

  update: async (data: { directories?: string[]; defaultDir?: string }) => {
    const response = await apiClient.put<void>('/settings/directories', data)
    return response.data
  },
}

// Options shared by every prune endpoint. `all` widens prune beyond
// dangling/anonymous (docker `-a`); `until` is an age filter like "24h".
// Each endpoint only honours the flags Docker supports for that resource.
export interface PruneOptions {
  all?: boolean
  until?: string
}

function pruneQuery(opts?: PruneOptions): string {
  const params = new URLSearchParams()
  if (opts?.all) params.set('all', 'true')
  if (opts?.until) params.set('until', opts.until)
  const qs = params.toString()
  return qs ? `?${qs}` : ''
}

/**
 * DeleteResult is the wire type for resource delete responses.
 *
 * Legacy shape (current backend): { deleted: unknown[] | string }
 * Action Truth Contract shape (post-B3 backend migration):
 *   { outcome, reason, details?: { untagged?, deleted? } }
 *
 * Use isActionResult() to branch during the migration window.
 */
export type DeleteResult = ActionResult<{
  untagged?: string[]
  deleted?: string[]
}> | { deleted: unknown[] | string }

/**
 * PruneResult is the wire type for resource prune responses.
 *
 * Legacy shape (current backend): { deleted: string[]; spaceReclaimed: number }
 * Action Truth Contract shape (post-B3 backend migration):
 *   { outcome, reason, details }
 *
 * Image prune details: { imagesDeleted, tagsRemoved, spaceReclaimed }
 * Volume/container/build-cache prune details: { deleted, spaceReclaimed }
 * Network prune details: { deleted }
 */
export type PruneResult = ActionResult<{
  // Image prune fields (classifyImagePruneReport)
  imagesDeleted?: number
  tagsRemoved?: number
  // Generic list field (volume/network/build-cache prune)
  deleted?: string[]
  // Shared space field
  spaceReclaimed?: number
}> | { deleted?: string[] | null; spaceReclaimed?: number | null }

/**
 * CreateStackResult is the wire type for stack create responses.
 *
 * Migrated backend (stack_crud.go):
 *   HTTP 201 success: { outcome:'success', reason, details:{stack, lintResults, deployed:true, deployOutput} }
 *   HTTP 207 partial: { outcome:'partial', reason, details:{stack, lintResults, deployed:false, deployError} }
 *   HTTP 4xx/5xx errors: AppError (no stack)
 *
 * All fields live inside `details`. Use isActionResult() to branch.
 * A 207 partial = stack created but not deployed (deploy failed).
 */
export type CreateStackResult = ActionResult<{
  stack: Stack
  lintResults?: LintResult[]
  deployed?: boolean
  deployOutput?: string
  deployError?: string
}>

/**
 * StackDeleteResult is the wire type for DELETE /stacks/:id.
 *
 * The backend (StacksHandler.Delete) renders truth.Success("stack deleted")
 * with details { id, output } — an ActionResult body, not a void 204.
 */
export type StackDeleteResult = ActionResult<{ id?: string; output?: string }>

export const resourcesApi = {
  images: async () => {
    const response = await apiClient.get<{ images: DockerImage[] }>('/resources/images')
    return response.data.images
  },
  deleteImage: async (id: string, force = false) => {
    const response = await apiClient.delete<DeleteResult>(`/resources/images/${encodeURIComponent(id)}?force=${force}`)
    return response.data
  },
  pruneImages: async (opts?: PruneOptions) => {
    const response = await apiClient.post<PruneResult>(`/resources/images/prune${pruneQuery(opts)}`)
    return response.data
  },

  inspectContainer: async (id: string) => {
    const response = await apiClient.get<Record<string, unknown>>(`/resources/containers/${encodeURIComponent(id)}/inspect`)
    return response.data
  },

  containers: async () => {
    const response = await apiClient.get<{ containers: unknown[] }>('/resources/containers')
    return response.data.containers
  },
  deleteContainer: async (id: string, force = false) => {
    const response = await apiClient.delete<DeleteResult>(`/resources/containers/${encodeURIComponent(id)}?force=${force}`)
    return response.data
  },
  startContainer: async (id: string) => {
    const response = await apiClient.post<{ message: string }>(`/resources/containers/${encodeURIComponent(id)}/start`)
    return response.data
  },
  stopContainer: async (id: string) => {
    const response = await apiClient.post<{ message: string }>(`/resources/containers/${encodeURIComponent(id)}/stop`)
    return response.data
  },
  restartContainer: async (id: string) => {
    const response = await apiClient.post<{ message: string }>(`/resources/containers/${encodeURIComponent(id)}/restart`)
    return response.data
  },
  pruneContainers: async (opts?: PruneOptions) => {
    const response = await apiClient.post<PruneResult>(`/resources/containers/prune${pruneQuery(opts)}`)
    return response.data
  },

  checkUpdates: async (refresh = false) => {
    const params = refresh ? { refresh: 'true' } : undefined
    const response = await apiClient.get<{
      updates: (ContainerUpdateInfo | CachedUpdate)[]
      fromCache?: boolean
      scannedAt?: string
      scanning?: boolean
    }>('/resources/updates', { params })
    return response.data
  },

  updateContainer: async (id: string) => {
    const response = await apiClient.post<{ jobId: string; wsUrl: string }>(`/resources/containers/${encodeURIComponent(id)}/update`)
    return response.data
  },

  updateStack: async (id: string) => {
    const response = await apiClient.post<{ jobId: string; wsUrl: string; noUpdates?: boolean }>(`/resources/stacks/${encodeURIComponent(id)}/update`)
    return response.data
  },

  getUpdateJobs: async () => {
    const response = await apiClient.get<{ jobs: import('@/stores/updateJobStore').UpdateJob[] }>('/resources/updates/jobs')
    return response.data
  },

  getUpdateJob: async (jobId: string) => {
    const response = await apiClient.get<import('@/stores/updateJobStore').UpdateJob>(`/resources/updates/jobs/${encodeURIComponent(jobId)}`)
    return response.data
  },

  getUpdateHistory: async (filters: UpdateHistoryFilters) => {
    const response = await apiClient.get<{
      entries: UpdateHistoryEntry[]
      total: number
      page: number
      limit: number
      totalPages: number
    }>('/resources/updates/history', { params: filters })
    return response.data
  },

  clearUpdateHistory: async (params?: { olderThan?: string; status?: string }) => {
    const response = await apiClient.delete<{ deleted: number }>('/resources/updates/history', { params })
    return response.data
  },

  volumes: async () => {
    const response = await apiClient.get<{ volumes: DockerVolume[] }>('/resources/volumes')
    return response.data.volumes
  },
  deleteVolume: async (name: string, force = false) => {
    const response = await apiClient.delete<DeleteResult>(`/resources/volumes/${encodeURIComponent(name)}?force=${force}`)
    return response.data
  },
  pruneVolumes: async (opts?: PruneOptions) => {
    const response = await apiClient.post<PruneResult>(`/resources/volumes/prune${pruneQuery(opts)}`)
    return response.data
  },

  networks: async () => {
    const response = await apiClient.get<{ networks: DockerNetwork[] }>('/resources/networks')
    return response.data.networks
  },
  createNetwork: async (input: { name: string; driver?: string; internal?: boolean; attachable?: boolean }) => {
    const response = await apiClient.post<ActionResult | { id: string; name: string }>('/resources/networks', input)
    return response.data
  },
  deleteNetwork: async (id: string) => {
    const response = await apiClient.delete<DeleteResult>(`/resources/networks/${encodeURIComponent(id)}`)
    return response.data
  },
  pruneNetworks: async (opts?: PruneOptions) => {
    const response = await apiClient.post<PruneResult>(`/resources/networks/prune${pruneQuery(opts)}`)
    return response.data
  },

  buildCache: async () => {
    const response = await apiClient.get<{ entries: BuildCacheEntry[] }>('/resources/build-cache')
    return response.data.entries
  },
  pruneBuildCache: async (opts?: PruneOptions) => {
    const response = await apiClient.post<PruneResult>(`/resources/build-cache/prune${pruneQuery(opts)}`)
    return response.data
  },
}

export const backupApi = {
  // Settings
  getSettings: async () => {
    const response = await apiClient.get<BackupSettings>('/settings/backup')
    return response.data
  },

  updateSettings: async (data: Partial<Pick<BackupSettings, 'repository' | 'keepDaily' | 'keepWeekly' | 'keepMonthly' | 'keepYearly' | 'autoPrune' | 'scheduleIntervalMinutes' | 'syncAfterBackup' | 'rcloneRemote' | 'rclonePath' | 'rcloneTransfers' | 'hostname'>> & { password?: string }) => {
    const response = await apiClient.put<BackupSettings>('/settings/backup', data)
    return response.data
  },

  // Policies (mirrors autoUpdateApi)
  getPolicies: async () => {
    const response = await apiClient.get<{ policies: BackupPolicy[] }>('/backups/policies')
    return response.data
  },

  setPolicy: async (stackId: string, data: { enabled: boolean; stopPolicy?: 'stop' | 'hot' }) => {
    const response = await apiClient.put<BackupPolicy>(
      `/backups/policies/stack/${encodeURIComponent(stackId)}`,
      data,
    )
    return response.data
  },

  deletePolicy: async (stackId: string) => {
    await apiClient.delete(`/backups/policies/stack/${encodeURIComponent(stackId)}`)
  },

  // Status & history
  getStatus: async () => {
    const response = await apiClient.get<BackupStatus>('/backups/status')
    return response.data
  },

  getHistory: async (limit = 50) => {
    const response = await apiClient.get<{ runs: BackupRun[] }>('/backups/history', { params: { limit } })
    return response.data
  },

  getRun: async (runId: string) => {
    const response = await apiClient.get<{ run: BackupRun; items: BackupRunItem[] }>(`/backups/runs/${encodeURIComponent(runId)}`)
    return response.data
  },

  // Snapshots
  listSnapshots: async (stackId: string) => {
    const response = await apiClient.get<BackupSnapshot[]>('/backups/snapshots', { params: { stackId } })
    return response.data
  },

  previewSnapshot: async (snapshotId: string) => {
    const response = await apiClient.get<{ entries: string[] }>(`/backups/snapshots/${encodeURIComponent(snapshotId)}/preview`)
    return response.data
  },

  // Operations — all return { runId, wsUrl } for streaming over WS
  runBackup: async (data?: { stackIds?: string[] | null; dryRun?: boolean }) => {
    const response = await apiClient.post<BackupOperationResult>('/backups/run', data ?? {})
    return response.data
  },

  runSync: async () => {
    const response = await apiClient.post<BackupOperationResult>('/backups/sync')
    return response.data
  },

  restore: async (data: { stackId: string; snapshotId: string; target?: string; confirm?: boolean }) => {
    const response = await apiClient.post<BackupOperationResult>('/backups/restore', data)
    return response.data
  },

  drRestore: async (data: { confirm: boolean }) => {
    const response = await apiClient.post<BackupOperationResult>('/backups/dr-restore', data)
    return response.data
  },

  initRepo: async () => {
    const response = await apiClient.post<{ initialized: boolean }>('/backups/repo/init')
    return response.data
  },

  testCloud: async () => {
    const response = await apiClient.post<{ ok: boolean }>('/backups/cloud/test')
    return response.data
  },

  prune: async (data?: { dryRun?: boolean }) => {
    const response = await apiClient.post<BackupOperationResult>('/backups/prune', data ?? {})
    return response.data
  },
}

export { apiClient }
