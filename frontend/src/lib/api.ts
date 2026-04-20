import axios, { AxiosError, type AxiosInstance } from 'axios'
import type {
  User,
  AuthResponse,
  Directory,
  ScanResult,
  Stack,
  StackStatus,
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
} from '@/types'

const API_BASE_URL = '/api/v1'

const apiClient: AxiosInstance = axios.create({
  baseURL: API_BASE_URL,
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
})

let getToken: (() => string | null) | null = null
let logout: (() => void) | null = null

apiClient.interceptors.request.use((config) => {
  const token = getToken?.()
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

apiClient.interceptors.response.use(
  (response) => response,
  (error: AxiosError<ApiError>) => {
    if (error.response?.status === 401 && logout) {
      const url = error.config?.url
      const isAuthEndpoint = url?.includes('/auth/status') || url?.includes('/auth/login')
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

  getGlobalEnv: async () => {
    const response = await apiClient.get<{ vars: Array<{ key: string; value: string }> }>('/settings/global-env')
    return response.data
  },

  getConfig: async () => {
    const response = await apiClient.get<{ stacksDir: string }>('/settings/config')
    return response.data
  },

  updateGlobalEnv: async (vars: Array<{ key: string; value: string }>) => {
    const response = await apiClient.put<void>('/settings/global-env', { vars })
    return response.data
  },
}

export const directoriesApi = {
  list: async () => {
    const response = await apiClient.get<{ directories: Directory[] }>('/directories')
    return response.data.directories
  },

  scan: async () => {
    const response = await apiClient.post<ScanResult>('/directories/scan')
    return response.data
  },

  get: async (path: string) => {
    const response = await apiClient.get<Directory>(`/directories/${encodeURIComponent(path)}`)
    return response.data
  },

  updateCredentials: async (path: string, data: {
    authType: string
    sshKeyPath?: string
    httpsUser?: string
    httpsToken?: string
  }) => {
    const response = await apiClient.put<{ directory: Directory }>(
      '/directories/credentials',
      { path, ...data },
    )
    return response.data.directory
  },
}

export const stacksApi = {
  list: async (status?: StackStatus) => {
    const params = status ? { status } : undefined
    const response = await apiClient.get<{ stacks: Stack[] }>('/stacks', { params })
    return response.data.stacks
  },

  create: async (input: {
    name: string
    composeContent: string
    envContent?: string
    deploy: boolean
  }) => {
    const response = await apiClient.post<{
      stack: Stack
      lintResults?: LintResult[]
      deployed?: boolean
      deployOutput?: string
    }>('/stacks', input)
    return response.data
  },

  lint: async (compose: string) => {
    const response = await apiClient.post<{ valid: boolean; lintResults: LintResult[] }>('/compose/lint', { compose })
    return response.data
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

  pull: async (id: string, restart = false) => {
    const response = await apiClient.post<CommandResult>(`/stacks/${encodeURIComponent(id)}/pull`, { restart })
    return response.data
  },

  delete: async (id: string) => {
    const response = await apiClient.delete<void>(`/stacks/${encodeURIComponent(id)}?confirm=true`)
    return response.data
  },
}

export const dashboardApi = {
  stats: async () => {
    const response = await apiClient.get<DashboardStats>('/dashboard/stats')
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

  getAutoUpdatePolicies: async () => {
    const response = await apiClient.get<{
      globalEnabled: boolean
      policies: AutoUpdatePolicy[]
    }>('/resources/auto-update/policies')
    return response.data
  },

  setAutoUpdatePolicy: async (targetType: string, targetId: string, data: { enabled: boolean }) => {
    const response = await apiClient.put<AutoUpdatePolicy>(
      `/resources/auto-update/policies/${targetType}/${encodeURIComponent(targetId)}`,
      data,
    )
    return response.data
  },

  deleteAutoUpdatePolicy: async (targetType: string, targetId: string) => {
    await apiClient.delete(`/resources/auto-update/policies/${targetType}/${encodeURIComponent(targetId)}`)
  },

  getUpdateSettings: async () => {
    const response = await apiClient.get<UpdateSettings>('/settings/updates')
    return response.data
  },

  updateUpdateSettings: async (data: { scanIntervalMinutes: number; globalAutoUpdate: boolean }) => {
    const response = await apiClient.put<UpdateSettings>('/settings/updates', data)
    return response.data
  },

  getGitSettings: async () => {
    const response = await apiClient.get<{ sshKey: string; httpsUser: string; hasHttpsToken: boolean }>('/settings/git')
    return response.data
  },

  updateGitSettings: async (data: { sshKey?: string; httpsUser?: string; httpsToken?: string }) => {
    const response = await apiClient.put<{ sshKey: string; httpsUser: string; hasHttpsToken: boolean }>('/settings/git', data)
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
  deleteNetwork: async (id: string) => {
    const response = await apiClient.delete<{ deleted: string }>(`/resources/networks/${encodeURIComponent(id)}`)
    return response.data
  },
  pruneNetworks: async () => {
    const response = await apiClient.post<{ deleted: string[] }>('/resources/networks/prune')
    return response.data
  },

  listBuildCache: async () => {
    const response = await apiClient.get<{ entries: BuildCacheEntry[] }>('/resources/build-cache')
    return response.data.entries
  },
  pruneBuildCache: async () => {
    const response = await apiClient.post<{ deleted: string[]; spaceReclaimed: number }>('/resources/build-cache/prune')
    return response.data
  },
}

export { apiClient }
