import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { RepositorySection } from '../backup-settings/RepositorySection'
import type { BackupSettings } from '@/types'

/**
 * The credential hint is the whole user-facing change of agent-os-r31s, and its
 * value is entirely in DISCRIMINATING. Both arms below run against the same
 * component with the same query; only the flag differs.
 */

function makeSettings(overrides: Partial<BackupSettings> = {}): BackupSettings {
  return {
    repository: '/data/restic-repo',
    repositorySource: 'db',
    hasPassword: true,
    passwordSource: 'db',
    keepDaily: 7,
    keepWeekly: 4,
    keepMonthly: 6,
    keepYearly: 0,
    autoPrune: true,
    scheduleIntervalMinutes: 0,
    syncAfterBackup: false,
    rcloneRemote: '',
    rclonePath: '',
    rcloneTransfers: 4,
    hostname: 'capstan',
    resticAvailable: true,
    rcloneAvailable: false,
    repositoryInitialized: true,
    scheduleMode: 'interval',
    scheduleTime: '03:00',
    scheduleDays: [0, 1, 2, 3, 4, 5, 6],
    serverTimezone: 'UTC',
    serverTimeOffset: '+00:00',
    ...overrides,
  }
}

function renderSection(settings: BackupSettings, repository: string) {
  return render(
    <RepositorySection
      settings={settings}
      repository={repository}
      onRepositoryChange={vi.fn()}
      password=""
      onPasswordChange={vi.fn()}
      showPassword={false}
      onTogglePasswordReveal={vi.fn()}
      onClearPassword={vi.fn()}
      isSaving={false}
      onInitRepo={vi.fn()}
      isInitializing={false}
    />,
  )
}

/** The hint, addressed the way a screen reader reaches it, not by its prose. */
function credentialHint() {
  return document.getElementById('backup-repository-credential-hint')
}

describe('RepositorySection credential hint', () => {
  it('renders the hint when the repository had a credential redacted out of it', () => {
    renderSection(
      makeSettings({
        repository: 'rest:https://***@backup.example.com/repo/',
        hasEmbeddedCredential: true,
      }),
      'rest:https://***@backup.example.com/repo/',
    )

    const hint = credentialHint()
    expect(hint).not.toBeNull()
    expect(hint!.textContent).toMatch(/credential is embedded/i)
    // It must say what to DO, not merely that something is hidden — the
    // operator's problem is that editing the path costs them the credential.
    expect(hint!.textContent).toMatch(/re-enter the full URI/i)
  })

  it('does NOT render the hint when the repository never had a credential', () => {
    renderSection(
      makeSettings({ repository: '/data/restic-repo', hasEmbeddedCredential: false }),
      '/data/restic-repo',
    )

    expect(credentialHint()).toBeNull()
    // Belt and braces: no stray copy about credentials anywhere in the section.
    expect(screen.queryByText(/credential is embedded/i)).toBeNull()
  })

  it('does NOT render the hint when the server omits the flag entirely', () => {
    // A server predating agent-os-r31s sends no flag. Absent must read as
    // "nothing hidden" rather than warning on every repository.
    renderSection(makeSettings({ repository: '/data/restic-repo' }), '/data/restic-repo')

    expect(credentialHint()).toBeNull()
  })

  it('leaves the input editable in both arms — the chosen shape is a hint, not a lock', () => {
    const { unmount } = renderSection(
      makeSettings({
        repository: 'rest:https://***@backup.example.com/repo/',
        hasEmbeddedCredential: true,
      }),
      'rest:https://***@backup.example.com/repo/',
    )

    const flagged = screen.getByLabelText(/repository path/i) as HTMLInputElement
    expect(flagged.readOnly).toBe(false)
    expect(flagged.disabled).toBe(false)
    // The hint is wired to the input, so a screen-reader user hears it on focus
    // rather than only sighted users seeing it.
    expect(flagged.getAttribute('aria-describedby')).toContain(
      'backup-repository-credential-hint',
    )
    unmount()

    renderSection(makeSettings({ hasEmbeddedCredential: false }), '/data/restic-repo')
    const plain = screen.getByLabelText(/repository path/i) as HTMLInputElement
    expect(plain.readOnly).toBe(false)
    expect(plain.getAttribute('aria-describedby')).not.toContain(
      'backup-repository-credential-hint',
    )
  })
})
