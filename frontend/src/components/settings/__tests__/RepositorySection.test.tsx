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
    repoState: 'ok',
    repoStateMessage: '',
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


/**
 * agent-os-ssqt. This section used to render a BOOLEAN -- "Initialized" /
 * "Not initialized" -- fed by a field that measured reachability. A repository
 * that existed but had gone unreachable therefore read "Not initialized" beside
 * an "Initialize repository" button: the destructive recovery offered for the
 * fault that calls for the opposite one.
 *
 * The arms below are deliberately two-sided on one instrument. `uninitialized`
 * must still say "Not initialized" and must still OFFER the button, or the fix
 * would have been bought by disabling everything.
 */
/**
 * The label is read by its PROSE, not by a test id, so an arm that fails does so
 * on the word actually on screen rather than on a missing hook. Exact string
 * matching is load-bearing: "Initialized" must not match "Not initialized".
 */
const REPO_STATE_LABELS = [
  'Initialized',
  'Not initialized',
  'Unreachable',
  'Unknown',
  'No password set',
  'Password rejected',
] as const

function repositoryStateText() {
  const found = REPO_STATE_LABELS.filter((label) => screen.queryByText(label) !== null)
  expect(found.length).toBe(1)
  return found[0]
}

function repositoryStateElement() {
  return screen.getByText(repositoryStateText())
}

function initButton() {
  return screen.getByRole('button', { name: /initialize repository/i }) as HTMLButtonElement
}

describe('RepositorySection repository state', () => {
  it('says Initialized only when the repository actually answered the probe', () => {
    renderSection(makeSettings({ repoState: 'ok' }), '/data/restic-repo')

    expect(repositoryStateText()).toBe('Initialized')
    // The server does NOT refuse here — it returns 200 {"initialized": true}
    // and creates nothing, so a press is a no-op dressed as an action.
    //
    // When this arm was written the no-op was also MISREPORTED: the toast in
    // useBackupActions read `initialized` and announced "Repository initialized
    // successfully" for an operation that did not run. Neither half of that is
    // still true — agent-os-nhiv deleted the `initialized` read (both of
    // repoInit's 200 paths are byte-identical, so it could never discriminate)
    // and the toast now states the resulting state instead of the action. This
    // assertion is unchanged and still correct; only its rationale narrowed,
    // from "disabled, or the operator is told something false" to "disabled,
    // because there is nothing here to create".
    expect(initButton().disabled).toBe(true)
  })

  it('says Not initialized and STILL offers Initialize when no repository exists', () => {
    // The control arm. This is the one state where creating is the right
    // recovery, and it must survive the change untouched.
    renderSection(makeSettings({ repoState: 'uninitialized' }), '/data/restic-repo')

    expect(repositoryStateText()).toBe('Not initialized')
    expect(initButton().disabled).toBe(false)
  })

  it('does NOT claim Not initialized when the repository merely went unreachable', () => {
    // The defect this bead exists for. A repository that holds every backup the
    // user has must not be reported as absent, and must not be offered for
    // creation -- the backend refuses it anyway (agent-os-81vr).
    renderSection(
      makeSettings({
        repoState: 'unreachable',
        repoStateMessage: 'repository not reachable: dial tcp: connection refused',
      }),
      '/data/restic-repo',
    )

    expect(repositoryStateText()).toBe('Unreachable')
    expect(screen.queryByText('Not initialized')).toBeNull()
    expect(initButton().disabled).toBe(true)
    // The cause is already on the wire and was consumed by nothing; surfacing
    // it is what turns "Unreachable" into something actionable.
    expect(repositoryStateElement().getAttribute('title')).toContain('connection refused')
  })

  it('names a missing restic password and refuses to initialize on it', () => {
    // agent-os-l04z. The DEFAULT state of a fresh install: both shipped compose
    // files leave RESTIC_PASSWORD commented out, so this is what a new operator
    // sees. It used to arrive as `unreachable` and be labelled Unreachable,
    // which asserts a probe result nobody obtained — restic was never run.
    renderSection(
      makeSettings({
        repoState: 'password_missing',
        repoStateMessage: 'no restic password is configured, so the repository was not contacted',
        resticAvailable: true,
      }),
      '/data/restic-repo',
    )

    expect(repositoryStateText()).toBe('No password set')
    expect(screen.queryByText('Unreachable')).toBeNull()
    expect(screen.queryByText('Not initialized')).toBeNull()
    expect(initButton().disabled).toBe(true)
    // The refusal must name THIS cause. The fall-through arm says the
    // repository "could not be read, so it may already exist", which is a non
    // sequitur when nothing was attempted — and a ternary chain has no
    // exhaustiveness check, so tsc stays green on exactly that mistake.
    expect(initButton().getAttribute('title')).toMatch(/set a restic password first/i)
    expect(initButton().getAttribute('title')).not.toMatch(/may already exist/i)
  })

  it('names a rejected password and refuses to initialize OVER the repository', () => {
    // restic exit 12. This is the destructive-adjacent one: a repository exists
    // here and holds every backup the user has, and only the key is wrong.
    // Enabling Initialize would create a new, empty repository beside it.
    renderSection(
      makeSettings({
        repoState: 'wrong_password',
        repoStateMessage: 'the configured restic password was rejected by the repository',
        resticAvailable: true,
      }),
      '/data/restic-repo',
    )

    expect(repositoryStateText()).toBe('Password rejected')
    expect(screen.queryByText('Not initialized')).toBeNull()
    expect(initButton().disabled).toBe(true)
    expect(initButton().getAttribute('title')).toMatch(/correct the password rather than initializing over it/i)
    expect(repositoryStateElement().getAttribute('title')).toContain('rejected by the repository')
  })

  it('still offers Initialize for uninitialized ONLY, with both new states present', () => {
    // The discriminating control for the two arms above. They show the button
    // disabled; without this one, a build that disabled it unconditionally
    // would pass both and be worse than the bug. Same instrument, same
    // fixture-shape, one field changed.
    renderSection(
      makeSettings({ repoState: 'uninitialized', resticAvailable: true }),
      '/data/restic-repo',
    )

    expect(initButton().disabled).toBe(false)
  })

  it('says Unknown when the settings naming the repository could not be read', () => {
    // restic is PRESENT here: the settings themselves are what could not be
    // read, so which repository is configured is itself unknown. Calling this
    // "Unreachable" would assert a probe result nobody obtained.
    renderSection(
      makeSettings({ repoState: 'settings_unreadable', resticAvailable: true }),
      '/data/restic-repo',
    )

    expect(repositoryStateText()).toBe('Unknown')
    expect(initButton().disabled).toBe(true)
  })

  it('says Unknown when restic is absent, the only state that ships an empty repoState', () => {
    // `resticAvailable: false ⟹ repoState: ''` is a backend INVARIANT, not a
    // race: CheckRepository returns before probing when restic is missing, and
    // the binary is fixed at construction. A fixture pairing '' with restic
    // present would assert a wire shape the server cannot emit AND would leave
    // initRefusal through its last branch where production takes its first.
    renderSection(makeSettings({ repoState: '', resticAvailable: false }), '/data/restic-repo')

    expect(repositoryStateText()).toBe('Unknown')
    expect(initButton().disabled).toBe(true)
    // The restic refusal outranks the repository state, and this is the branch
    // production actually reaches. Its discriminating partner is the enabled
    // arm above: restic PRESENT + 'uninitialized' leaves the button live, so
    // these two together show which condition did the disabling.
    expect(initButton().getAttribute('title')).toMatch(/restic not available/i)
  })

  /**
   * Drift arms. Unreachable under the TYPE — deleting the index fallback makes
   * tsc report TS7053, so the lookup is total — but reachable at RUNTIME two
   * ways: a cached SPA bundle talking to a backend that has added a fifth
   * state, and a response missing the key altogether. Both must fail CLOSED.
   */
  describe('when the wire drifts from the type', () => {
    function withoutRepoState() {
      const settings = makeSettings()
      delete (settings as Partial<BackupSettings>).repoState
      return settings
    }

    it('renders Unknown for a state this build has never heard of', () => {
      renderSection(
        makeSettings({ repoState: 'quantum_superposition' as BackupSettings['repoState'] }),
        '/data/restic-repo',
      )

      expect(repositoryStateText()).toBe('Unknown')
    })

    it('renders Unknown when the key is absent altogether', () => {
      renderSection(withoutRepoState(), '/data/restic-repo')

      expect(repositoryStateText()).toBe('Unknown')
    })

    it('leaves Initialize DISABLED when the key is absent', () => {
      // Failing closed is the whole point: an absent key must not be read as
      // "uninitialized" and offered the one action that can lose data.
      renderSection(withoutRepoState(), '/data/restic-repo')

      expect(initButton().disabled).toBe(true)
    })
  })
})
