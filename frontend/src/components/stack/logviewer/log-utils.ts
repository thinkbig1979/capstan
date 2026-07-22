import type { LogLevel } from './types'

export function getLogLevel(message: string): LogLevel {
  const upperMsg = message.toUpperCase()
  if (upperMsg.includes('ERROR') || upperMsg.includes('FATAL') || upperMsg.includes('PANIC')) {
    return 'error'
  }
  if (upperMsg.includes('WARN') || upperMsg.includes('WARNING')) {
    return 'warn'
  }
  return 'other'
}

export function getLogLevelColor(message: string): string {
  switch (getLogLevel(message)) {
    case 'error':
      return 'text-destructive'
    case 'warn':
      return 'text-warning'
    default:
      return ''
  }
}

export function isEditableTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

export function escapeRegExp(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
