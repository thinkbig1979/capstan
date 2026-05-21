import axios, { AxiosError, type AxiosInstance } from 'axios'
import type {
  User,
  AuthResponse,
  ConfiguredDir,
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
  UpdateHistoryFilters,
  GitStatus,
  GitCommit,
} from '@/types'

const API_BASE_URL = '/api/v1'

function getCSRFToken(): string | null {
  const match = document.cookie.match(/(?:^|;\s*)docker_manager_csrf=([^;]*)/)
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
    const safeError = error.response?.data || { error: 'Unknown error', code: 'UNKNOWN' }
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

  getAuditLog: async (page = 1, pageSize = 50) => {
    const response = await apiClient.get<{
      entries: Array<{ id: string; userId: string; stackId: string; action: string; detail: string; createdAt: string }>
      total: number
      page: number
      pageSize: number
    }>('/settings/audit-log', { params: { page, pageSize } })
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

export const gitApi = {
  status: async (stackId: string) => {
    const response = await apiClient.get<GitStatus>(`/git?stackId=${encodeURIComponent(stackId)}`)
    return response.data
  },

  pull: async (stackId: string, redeploy = false) => {
    const response = await apiClient.post<{ success: boolean; previousCommit: string; currentCommit: string; changedFiles: string[]; redeployedStacks: string[] }>(`/git/pull?stackId=${encodeURIComponent(stackId)}&redeploy=${redeploy}`)
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
    const response = await apiClient.post<CommandResult>(`/stacks/${encodeURIComponent(id)}/start`)
    return response.data
  },

  stop: async (id: string) => {
    const response = await apiClient.post<CommandResult>(`/stacks/${encodeURIComponent(id)}/stop`)
    return response.data
  },

  restart: async (id: string) => {
    const response = await apiClient.post<CommandResult>(`/stacks/${encodeURIComponent(id)}/restart`)
    return response.data
  },

  pull: async (id: string) => {
    const response = await apiClient.post<CommandResult>(`/stacks/${encodeURIComponent(id)}/pull`)
    return response.data
  },

  delete: async (id: string) => {
    const response = await apiClient.delete<void>(`/stacks/${encodeURIComponent(id)}`)
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

  updateEnv: async (id: string, content: string) => {
    const response = await apiClient.put<void>(`/stacks/${encodeURIComponent(id)}/env`, { content })
    return response.data
  },

  create: async (input: { name: string; directory?: string; composeContent: string; envContent?: string; deploy: boolean }) => {
    const response = await apiClient.post<{ stack: Stack; stackId: string; lintResults?: LintResult[]; deployed?: boolean; deployOutput?: string }>('/stacks', input)
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

  updateCredentials: async (directoryPath: string, credentials: { authType?: string; sshKeyPath?: string; httpsUser?: string; httpsToken?: string }) => {
    const response = await apiClient.put<void>(`/directories/${encodeURIComponent(directoryPath)}/credentials`, credentials)
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

export const resourcesApi = {
  images: async () => {
    const response = await apiClient.get<{ images: DockerImage[] }>('/resources/images')
    return response.data.images
  },
  deleteImage: async (id: string, force = false) => {
    const response = await apiClient.delete<{ deleted: unknown[] }>(`/resources/images/${encodeURIComponent(id)}?force=${force}`)
    return response.data
  },
  pruneImages: async () => {
    const response = await apiClient.post<{ deleted: string[]; spaceReclaimed: number }>('/resources/images/prune')
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
    const response = await apiClient.delete<{ deleted: string }>(`/resources/containers/${encodeURIComponent(id)}?force=${force}`)
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
  pruneContainers: async () => {
    const response = await apiClient.post<{ deleted: string[]; spaceReclaimed: number }>('/resources/containers/prune')
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
    const response = await apiClient.post<{ message: string; historyId?: string; oldDigest?: string; newDigest?: string; durationMs?: number }>(`/resources/containers/${encodeURIComponent(id)}/update`)
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
    const response = await apiClient.delete<{ deleted: string }>(`/resources/volumes/${encodeURIComponent(name)}?force=${force}`)
    return response.data
  },
  pruneVolumes: async () => {
    const response = await apiClient.post<{ deleted: string[]; spaceReclaimed: number }>('/resources/volumes/prune')
    return response.data
  },

  networks: async () => {
    const response = await apiClient.get<{ networks: DockerNetwork[] }>('/resources/networks')
    return response.data.networks
  },
  createNetwork: async (input: { name: string; driver?: string; internal?: boolean; attachable?: boolean }) => {
    const response = await apiClient.post<{ id: string; name: string }>('/resources/networks', input)
    return response.data
  },
  deleteNetwork: async (id: string) => {
    const response = await apiClient.delete<{ deleted: string }>(`/resources/networks/${encodeURIComponent(id)}`)
    return response.data
  },
  pruneNetworks: async () => {
    const response = await apiClient.post<{ deleted: string[] }>('/resources/networks/prune')
    return response.data
  },

  buildCache: async () => {
    const response = await apiClient.get<{ entries: BuildCacheEntry[] }>('/resources/build-cache')
    return response.data.entries
  },
  pruneBuildCache: async () => {
    const response = await apiClient.post<{ deleted: string[]; spaceReclaimed: number }>('/resources/build-cache/prune')
    return response.data
  },
}

export { apiClient }
