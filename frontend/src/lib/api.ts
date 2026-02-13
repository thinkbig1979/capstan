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

const apiClient: AxiosInstance = axios.create({
  baseURL: API_BASE_URL,
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
      logout()
      window.location.href = '/login'
    }
    return Promise.reject(error.response?.data || { error: 'Unknown error', code: 'UNKNOWN' })
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
