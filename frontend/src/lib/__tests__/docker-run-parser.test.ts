import { describe, it, expect, vi } from 'vitest'

vi.mock('composerize', () => ({
  default: (input: string) => `services:\n  app:\n    image: nginx\n# ${input}`,
}))

import { isDockerRunCommand, convertDockerRun } from '../docker-run-parser'

describe('isDockerRunCommand', () => {
  it('returns true for "docker run"', () => {
    expect(isDockerRunCommand('docker run nginx')).toBe(true)
  })

  it('returns true for leading whitespace', () => {
    expect(isDockerRunCommand('  docker run nginx')).toBe(true)
  })

  it('returns true case insensitive', () => {
    expect(isDockerRunCommand('docker RUN nginx')).toBe(true)
  })

  it('returns true with leading asterisk', () => {
    expect(isDockerRunCommand('* docker run nginx')).toBe(true)
  })

  it('returns false for "docker build"', () => {
    expect(isDockerRunCommand('docker build -t app .')).toBe(false)
  })

  it('returns false for empty string', () => {
    expect(isDockerRunCommand('')).toBe(false)
  })
})

describe('convertDockerRun', () => {
  it('converts a simple docker run command', () => {
    const result = convertDockerRun('docker run nginx')
    expect(result).toContain('services:')
  })

  it('handles multiline input', () => {
    const input = `docker run \\
      -p 8080:80 \\
      nginx`
    const result = convertDockerRun(input)
    expect(result).toContain('services:')
  })
})
