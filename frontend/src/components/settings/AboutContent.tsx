import { ExternalLink } from 'lucide-react'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { formatDateFull } from '@/lib/format'
import { useVersion } from '@/hooks/useVersion'

const RELEASES_BASE = 'https://github.com/thinkbig1979/capstan/releases/tag'

/** Semver, as produced by docker/metadata-action from a `v1.2.3` git tag. A
 *  manual dispatch build is `manual-<sha>` and an unstamped build is `dev`;
 *  neither has a release page, so neither gets a link. */
const SEMVER = /^\d+\.\d+\.\d+/

/** Values stamped at link time, so "unknown" means this build carried no
 *  build-arg — not that the value is missing. Say so rather than showing a bare
 *  placeholder that reads as an error. */
function displayOrDash(value: string | undefined): string {
  if (!value || value === 'unknown') return '—'
  return value
}

export function AboutContent() {
  const { data, isLoading, isError } = useVersion()

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <LoadingSpinner size="small" />
        Reading build identity...
      </div>
    )
  }

  if (isError || !data) {
    return (
      <p className="text-sm text-muted-foreground">
        Could not read the build identity from the server.
      </p>
    )
  }

  const isRelease = SEMVER.test(data.version)

  return (
    <div className="space-y-4">
      <dl className="grid grid-cols-[8rem_1fr] gap-x-4 gap-y-3 text-sm">
        <dt className="text-muted-foreground">Version</dt>
        <dd className="font-mono break-all" data-testid="about-version">
          {data.version}
        </dd>

        <dt className="text-muted-foreground">Commit</dt>
        <dd className="font-mono break-all" data-testid="about-commit">
          {displayOrDash(data.commit)}
        </dd>

        <dt className="text-muted-foreground">Built</dt>
        <dd data-testid="about-build-date">
          {data.buildDate && data.buildDate !== 'unknown'
            ? formatDateFull(data.buildDate)
            : '—'}
        </dd>
      </dl>

      {data.version === 'dev' && (
        <p className="text-sm text-muted-foreground">
          This binary was built without release metadata — a local build rather than a
          published image.
        </p>
      )}

      {isRelease && (
        <a
          href={`${RELEASES_BASE}/v${data.version}`}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1.5 text-sm text-primary hover:underline"
        >
          Release notes for v{data.version}
          <ExternalLink className="h-3.5 w-3.5" />
        </a>
      )}
    </div>
  )
}
