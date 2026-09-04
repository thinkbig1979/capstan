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

  // The two-sided arm for staleness: the hint must describe the value ON SCREEN,
  // not the one the server sent. Same instrument, same settings object; only the
  // draft differs between the arms.
  describe('once the operator edits the field', () => {
    const remote = 'rest:https://***@backup.example.com/repo/'
    const flagged = () =>
      makeSettings({ repository: remote, hasEmbeddedCredential: true })

    it('renders the hint while the field still holds the redacted value', () => {
      renderSection(flagged(), remote)
      expect(credentialHint()).not.toBeNull()
    })

    it('drops the hint once the draft diverges from what the server sent', () => {
      // The operator has typed a real URI. The field now holds their cleartext
      // credential and no "***" at all, so a hint saying one is hidden behind
      // "***" would be false about the value they are looking at.
      renderSection(flagged(), 'rest:https://bob:BRANDNEWSECRET@backup.example.com/repo/')
      expect(credentialHint()).toBeNull()
    })

    it('drops the hint for a mere path edit, which is when the 422 bites', () => {
      renderSection(flagged(), 'rest:https://***@backup.example.com/repo-renamed/')
      expect(credentialHint()).toBeNull()
    })

    it('restores the hint if the original value is typed back', () => {
      // Not a curiosity: the field holds "***" again, so saving it WOULD be
      // rejected, and the hint is the thing that says so.
      const { unmount } = renderSection(flagged(), 'something else')
      expect(credentialHint()).toBeNull()
      unmount()
      renderSection(flagged(), remote)
      expect(credentialHint()).not.toBeNull()
    })
  })

  it.each([
    ['an empty userinfo', 'http://@host/path'],
    ["restic's documented SFTP form", 'sftp:user@host:/srv/restic-repo'],
  ])(
    'does NOT render the hint for %s — an @ in the value is not the trigger, the flag is',
    (_label, repository) => {
      // The backend already decides this: neither value has anything redacted
      // out of it, so the flag is false. The arm that matters here is that the
      // COMPONENT keys off the flag and never off the string it is rendering —
      // a component that sniffed for "@" itself would warn about a field
      // hiding nothing, which is the false marker the backend takes care to
      // avoid.
      renderSection(makeSettings({ repository, hasEmbeddedCredential: false }), repository)

      expect(credentialHint()).toBeNull()
      // The value is still shown in full, so this is not passing by hiding it.
      expect((screen.getByLabelText(/repository path/i) as HTMLInputElement).value).toBe(
        repository,
      )
    },
  )

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
