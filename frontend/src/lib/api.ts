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
} from '@/types'

const API_BASE_URL = '/api/v1'

const getCSRFToken = () => {
  const meta = document.querySelector('meta[name="csrf-token"]')
  return meta?.getAttribute('content')
}

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
      logout()
      window.location.href = '/login'
    }
    const safeError = error.response?.data || { error: 'Unknown error', code: 'UNKNOWN' }
    return Promise.reject(safeError)
  },
)

export const safeErrorMessage = (error: any): string => {
  if (error?.response?.status === 401) return 'Authentication failed'
  if (error?.response?.status === 403) return 'Access denied'
  if (error?.response?.status === 404) return 'Resource not found'
  if (error?.response?.status === 429) return 'Too many requests'
  if (error?.response?.status === 500) return 'Server error occurred'
  if (error?.response?.status === 503) return 'Service unavailable'
  return 'An error occurred'
}

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

  updateGlobalEnv: async (vars: Array<{ key: string; value: string }>) => {
    const response = await apiClient.put<void>('/settings/global-env', { vars })
    return response.data
  },
}

export const directoriesApi = {
  list: async () => {
    const response = await apiClient.get<Directory[]>('/directories')
    return response.data
  },

  scan: async () => {
    const response = await apiClient.post<ScanResult>('/directories/scan')
    return response.data
  },

  get: async (path: string) => {
    const response = await apiClient.get<Directory>(`/directories/${encodeURIComponent(path)}`)
    return response.data
  },
}

export const stacksApi = {
  list: async (status?: StackStatus) => {
    const params = status ? { status } : undefined
    const response = await apiClient.get<Stack[]>('/stacks', { params })
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

export default apiClient
