import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { HelpHint } from '@/components/ui/help-hint'
import { AlertTriangle, CheckCircle2, Eye, EyeOff, KeyRound, XCircle } from 'lucide-react'
import type { BackupSettings } from '@/types'
import { SourceBadge } from './SourceBadge'

/**
 * agent-os-ssqt. This used to be a boolean fed by a field that measured
 * REACHABILITY under a name asserting INITIALISATION, so a repository that
 * existed but had gone unreachable rendered "Not initialized" beside a button
 * offering to create one — the destructive recovery for the fault that calls
 * for the opposite. The question this section asks is "does a repository
 * EXIST", which is not the question the dashboard asks ("can we back up right
 * now"), so it reads repoState directly rather than collapsing it again.
 *
 * `''` is "nothing probed it" and is genuinely on the wire, not a placeholder:
 * both handlers build their response as a Go map, so the struct tag that would
 * omit the key never applies. It is reported as unknown rather than as a probe
 * result nobody obtained.
 *
 * `''` and `settings_unreadable` share the word "Unknown" but deliberately not
 * the colour. `settings_unreadable` is a FAULT — the settings should have been
 * readable and were not — so it is amber like `unreachable`. `''` only ever
 * occurs with restic absent, which is an unconfigured install rather than a
 * fault, and the banner above already says restic is missing; amber here would
 * dress a normal pre-install state as a problem.
 *
 * `password_missing` is the same judgement applied again, and it is the reason
 * it is NOT amber. It is the default state of a fresh install — both shipped
 * compose files leave RESTIC_PASSWORD commented out — so it is an unfinished
 * setup rather than something that broke, and the password field it points at
 * is directly above. `wrong_password` IS amber: a password was configured and
 * the repository rejected it, which means something is wrong right now.
 *
 * Neither says "Unreachable". The repository is reachable in both cases —
 * untested in the first because no credential existed to try, and demonstrably
 * so in the second — and sending an operator to check a remote and a mount that
 * are working is the defect agent-os-l04z was filed for.
 *
 * The `satisfies Record<BackupSettings['repoState'], unknown>` below is what
 * makes adding a state to the union a COMPILE ERROR here rather than a silent
 * fallback. Keep it.
 */
const REPO_STATE_DISPLAY = {
  ok: { label: 'Initialized', Icon: CheckCircle2, className: 'text-success font-medium' },
  uninitialized: { label: 'Not initialized', Icon: XCircle, className: 'text-muted-foreground' },
  unreachable: { label: 'Unreachable', Icon: AlertTriangle, className: 'text-warning font-medium' },
  settings_unreadable: { label: 'Unknown', Icon: AlertTriangle, className: 'text-warning font-medium' },
  password_missing: {
    label: 'No password set',
    Icon: KeyRound,
    className: 'text-muted-foreground',
  },
  wrong_password: {
    label: 'Password rejected',
    Icon: AlertTriangle,
    className: 'text-warning font-medium',
  },
  '': { label: 'Unknown', Icon: AlertTriangle, className: 'text-muted-foreground' },
} as const satisfies Record<BackupSettings['repoState'], unknown>

interface RepositorySectionProps {
  settings: BackupSettings
  repository: string
  onRepositoryChange: (value: string) => void
  password: string
  onPasswordChange: (value: string) => void
  showPassword: boolean
  onTogglePasswordReveal: () => void
  onClearPassword: () => void
  isSaving: boolean
  onInitRepo: () => void
  isInitializing: boolean
}

export function RepositorySection({
  settings,
  repository,
  onRepositoryChange,
  password,
  onPasswordChange,
  showPassword,
  onTogglePasswordReveal,
  onClearPassword,
  isSaving,
  onInitRepo,
  isInitializing,
}: RepositorySectionProps) {
  // The flag describes settings.repository — the value the SERVER sent. The
  // input renders `repository`, the draft. Once the operator types a
  // replacement the two diverge, and a hint gated on the flag alone would keep
  // asserting that a credential is hidden behind "***" in a field that now
  // holds their freshly typed cleartext URI and no marker at all — wrong at
  // exactly the moment they are acting on it.
  //
  // Compared against the same `?? ''` normalisation toDraft uses to seed the
  // field, so an untouched form matches instead of reading as edited. Typing
  // the original value back restores the hint, which is correct: the field then
  // holds "***" again and saving it would be rejected.
  const showCredentialHint =
    settings.hasEmbeddedCredential === true && repository === (settings.repository ?? '')

  const repoStateDisplay = REPO_STATE_DISPLAY[settings.repoState] ?? REPO_STATE_DISPLAY['']
  // Creating is correct for exactly one state, and the other FIVE are refused
  // for THREE different reasons, so they cannot share one sentence.
  //
  // On `ok` the server does NOT refuse: handlers/backup.go returns 200
  // {"initialized": true} and creates nothing. The button is disabled because
  // there is nothing to create — a press here is a no-op dressed as an action.
  //
  // That used to be the WEAKER half of the argument, because the toast then
  // announced "Repository initialized successfully" for the no-op, so disabling
  // the button was also the only thing standing between the operator and a
  // false claim. agent-os-nhiv removed the false claim at its source: both of
  // repoInit's 200 paths are byte-identical and the frontend cannot tell the
  // no-op from a real initialisation, so the toast now states the resulting
  // STATE, which is true either way. Disabling here is no longer load-bearing
  // for honesty; it stands on its own, that there is nothing to create.
  //
  // On `unreachable`, `settings_unreadable`, `wrong_password` and
  // `password_missing` the server refuses with 503 BACKUP_REPO_UNREACHABLE —
  // repoInit's guard is `RepoState != RepoStateUninitialized`, so all four take
  // that one branch. The reason is data loss rather than tidiness for the first
  // three: a repository that could not be read may well exist and hold every
  // backup the user has, one that rejected a password certainly does, and when
  // the settings themselves are unreadable we do not know WHICH repository is
  // configured. Initialising in any of them points every later backup at a new,
  // empty repository while the real one still exists. `password_missing` shares
  // the status but not the reason: nothing was attempted at all, there is no
  // credential with which to create or read a repository, and the field that
  // fixes it is on this same form.
  //
  // On `''` the server answers 409 BACKUP_UNAVAILABLE, NOT 503, and it never
  // reaches the repo-state guard at all: repoInit's `!av.ResticPresent` check
  // returns before CheckRepository is called, which is the same reason `''`
  // means "not probed". This distinction is why the refusal below tests
  // `!settings.resticAvailable` FIRST — that is the branch production takes,
  // and the state-keyed arms never see `''`.
  //
  // The gate stays `=== 'uninitialized'` ONLY. Neither credential state may
  // enable it: initialising over a repository whose password is merely wrong
  // would create a new, empty one beside a repository holding every backup the
  // user has, which is precisely the destructive-adjacent action agent-os-81vr
  // exists to prevent. The backend refuses both independently (repoInit's guard
  // is a negative test on this same state), so this is defence in depth.
  const canInitialize = settings.resticAvailable && settings.repoState === 'uninitialized'
  // Each refused state gets its OWN sentence. There is no exhaustiveness check
  // on a ternary chain and tsc stays green on it however many states exist, so
  // the fall-through arm silently claims whatever it last said — and what it
  // said, "the repository could not be read, so it may already exist", is a non
  // sequitur for a missing password, where nothing was read because nothing was
  // attempted.
  const initRefusal = !settings.resticAvailable
    ? 'restic not available'
    : settings.repoState === 'ok'
      ? 'This repository is already initialized'
      : settings.repoState === 'uninitialized'
        ? undefined
        : settings.repoState === 'password_missing'
          ? 'Set a restic password first — without one the repository cannot be created or read'
          : settings.repoState === 'wrong_password'
            ? 'A repository exists here and rejected this password — correct the password rather than initializing over it'
            : 'The repository could not be read, so it may already exist — initializing is refused'

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-1.5">
        <h3 className="text-lg font-medium">Repository</h3>
        <HelpHint
          label="restic repository"
          title="restic repository"
          side="right"
          href="https://github.com/thinkbig1979/capstan/blob/main/docs/how-to/configure-backups.md#configuration"
        >
          <p>
            Backups run through restic, which stores them deduplicated and encrypted in a
            repository.
          </p>
          <p>
            Give it a path inside the container and a password, then initialize it once before
            the first backup.
          </p>
        </HelpHint>
      </div>

      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <Label htmlFor="backup-repository">Repository path</Label>
          <SourceBadge source={settings.repositorySource} />
        </div>
        <Input
          id="backup-repository"
          type="text"
          placeholder="/data/restic-repo (inside container)"
          value={repository}
          onChange={(e) => onRepositoryChange(e.target.value)}
          className="max-w-md"
          aria-describedby={
            showCredentialHint
              ? 'backup-repository-credential-hint backup-repository-hint'
              : 'backup-repository-hint'
          }
        />
        {/*
          The field stays an ordinary editable input; this is the only thing that
          tells the operator a credential is hidden inside the value they are
          looking at. Gated on the flag, never always-on: a warning that shows on
          every repository is one operators learn to skip past, which costs more
          than it saves on the repositories that do carry a credential.
        */}
        {showCredentialHint && (
          <p
            id="backup-repository-credential-hint"
            className="flex items-start gap-1.5 text-xs text-warning"
          >
            <KeyRound className="h-3.5 w-3.5 mt-0.5 shrink-0" aria-hidden="true" />
            <span>
              A credential is embedded in this value and is shown here as <code>***</code>. To
              change any part of it, re-enter the full URI including the credential. Saving while{' '}
              <code>***</code> is still in the field is rejected, so the stored credential stays
              intact.
            </span>
          </p>
        )}
        <p id="backup-repository-hint" className="text-xs text-muted-foreground">
          Local path for the restic repository. Must be accessible inside the container.
          Leave blank to revert to the <code>RESTIC_REPOSITORY</code> environment variable.
        </p>
      </div>

      {/* Password field — masked behind env-unlock */}
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <Label htmlFor="backup-password">
            Repository password
            {settings.hasPassword && (
              <span className="ml-1 text-xs text-muted-foreground font-normal">
                (currently set)
              </span>
            )}
          </Label>
          <SourceBadge source={settings.passwordSource} />
        </div>
        <div className="flex items-center gap-2 max-w-md">
          <Input
            id="backup-password"
            type={showPassword ? 'text' : 'password'}
            placeholder={settings.hasPassword ? 'Leave blank to keep current password' : 'Enter restic password'}
            value={password}
            onChange={(e) => onPasswordChange(e.target.value)}
            className="flex-1"
            aria-describedby="backup-password-hint"
          />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={onTogglePasswordReveal}
            title={showPassword ? 'Hide password' : 'Reveal password'}
            aria-label={showPassword ? 'Hide backup password' : 'Reveal backup password'}
          >
            {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
          </Button>
        </div>
        <p id="backup-password-hint" className="text-xs text-muted-foreground">
          Leave blank to keep the current password unchanged. Corresponds to{' '}
          <code>RESTIC_PASSWORD</code>.
        </p>
        {settings.hasPassword && settings.passwordSource === 'db' && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="text-destructive hover:text-destructive px-0 h-auto"
            onClick={onClearPassword}
            disabled={isSaving}
          >
            Clear saved password
          </Button>
        )}
      </div>

      {/* Init repo + status */}
      <div className="flex items-center gap-3 flex-wrap">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onInitRepo}
          disabled={isInitializing || !canInitialize}
          title={initRefusal}
        >
          {isInitializing ? (
            <>
              <span className="mr-2"><LoadingSpinner size="small" /></span>
              Initializing…
            </>
          ) : (
            'Initialize repository'
          )}
        </Button>
        <span
          className={`inline-flex items-center gap-1.5 text-sm ${repoStateDisplay.className}`}
          title={settings.repoStateMessage || undefined}
        >
          <repoStateDisplay.Icon className="h-4 w-4" />
          {repoStateDisplay.label}
        </span>
      </div>
    </div>
  )
}
