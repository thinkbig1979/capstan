import type { ComposeStackFields } from '@/components/dashboard/unmanaged-compose'

// Shown on the row itself: the project, where compose says it lives (its own
// labels, never a guess), and the one edit that brings it under management.
// ScanAll picks the directory up after the restart, so there is no Adopt step.
export function UnmanagedComposeNote({ c }: { c: ComposeStackFields }) {
  return (
    <div className="mt-0.5 max-w-[320px] space-y-0.5 text-xs text-muted-foreground">
      <p>
        <span>{c.projectName}</span> · not managed by Capstan
      </p>
      {c.composeWorkingDir ? (
        <p className="font-mono break-all">{c.composeWorkingDir}</p>
      ) : (
        <p>Compose did not record this project&apos;s directory.</p>
      )}
      {c.composeConfigFiles && <p className="font-mono break-all">{c.composeConfigFiles}</p>}
      <p>
        To manage it, mount {c.composeWorkingDir ? 'this path' : "the project's compose directory"} into
        Capstan, add it to <code>EXTRA_STACKS_DIRS</code>, then restart Capstan.
      </p>
    </div>
  )
}
