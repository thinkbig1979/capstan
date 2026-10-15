import { formatBytes } from '@/lib/format'
import type { DockerCleanupCandidate, DockerCleanupPreview } from '@/types'
import type { HostCandidateItem } from '@/types/generated-services'
import { joinNames } from './categories'

/** What to show for one image row.
 *
 *  An unused tagged image carries its tag. A dangling one has a `repository`
 *  only sometimes: it is ABSENT for the fully-untagged `<none>:<none>` form,
 *  which is what every locally built superseded image becomes — the common
 *  case. So the id is the fallback, formatted the way the rest of the app
 *  formats one (ImagesTab.tsx): drop the algorithm prefix FIRST, then truncate.
 *  A raw slice(0, 12) of `sha256:abc…` yields `sha256:abcde`, which is mostly
 *  prefix and does not distinguish two images. */
function candidateLabel(candidate: DockerCleanupCandidate): string {
  return candidate.tag || candidate.repository || candidate.id.replace('sha256:', '').substring(0, 19)
}

function itemLine(item: HostCandidateItem): string {
  const parts = [item.label]
  if (item.detail) parts.push(item.detail)
  return parts.join(' · ')
}

function ItemSection({ title, items, sized }: { title: string; items: HostCandidateItem[]; sized: boolean }) {
  if (items.length === 0) return null
  return (
    <div className="space-y-1">
      <p className="text-sm font-medium">
        {title} ({items.length})
      </p>
      <ul className="space-y-1">
        {items.map((item) => (
          <li key={item.id} className="flex items-center justify-between gap-4 font-mono text-xs">
            <span className="truncate">{itemLine(item)}</span>
            {sized && (
              <span className="shrink-0 text-muted-foreground">
                {item.size === undefined ? 'size unknown' : formatBytes(item.size)}
              </span>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}

/** What the next run would remove, per category. Every figure names the floor
 *  the SERVER echoed, never one typed elsewhere: the list belongs to the floor
 *  it was actually computed at. */
export function CleanupPreviewList({ preview }: { preview: DockerCleanupPreview }) {
  const images = preview.candidates
  const withheld = preview.withheldStacks ?? []
  return (
    <div className="space-y-3">
      {withheld.length > 0 && (
        // Same first sentence as the Free up space dialog's notice. The second
        // differs on purpose: dangling images and build cache are still listed
        // here, so "only build cache is listed" would be false.
        <p data-testid="cleanup-preview-withheld" className="rounded-md border border-warning/40 bg-warning/10 p-3 text-sm">
          Capstan could not read the compose file of {joinNames(withheld)}. Until it can, no unused tagged image,
          container, volume or network is removed: those stacks could use any of them.
        </p>
      )}

      {images.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No image to remove: none on the list was created more than {preview.minAgeHours} hours ago.
        </p>
      ) : (
        <div className="space-y-1">
          <p className="text-sm">
            {images.length} image{images.length === 1 ? '' : 's'} created more than {preview.minAgeHours} hours
            ago, reclaiming {formatBytes(preview.reclaimableBytes)}.
          </p>
          <ul className="space-y-1">
            {images.map((candidate) => (
              <li key={candidate.id} className="flex items-center justify-between gap-4 font-mono text-xs">
                <span className="truncate">{candidateLabel(candidate)}</span>
                <span className="shrink-0 text-muted-foreground">{formatBytes(candidate.size)}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Shown only when a run would prune some; a run records it apart from the images. */}
      {preview.cacheReclaimableBytes > 0 && (
        <p className="text-sm" data-testid="cleanup-cache-reclaimable">
          Unused build cache older than {preview.minAgeHours} hours:{' '}
          {formatBytes(preview.cacheReclaimableBytes)}.
        </p>
      )}

      <ItemSection title="Stopped containers" items={preview.containers} sized />
      <ItemSection title="Unattached volumes" items={preview.volumes} sized />
      <ItemSection title="Unused networks" items={preview.networks} sized={false} />

      {preview.otherReclaimableBytes > 0 && (
        <p className="text-sm" data-testid="cleanup-other-reclaimable">
          Containers and volumes: {formatBytes(preview.otherReclaimableBytes)}.
        </p>
      )}
      {preview.unknownSizeCount > 0 && (
        <p className="text-xs text-muted-foreground" data-testid="cleanup-unknown-size">
          {preview.unknownSizeCount} item{preview.unknownSizeCount === 1 ? '' : 's'} of unknown size, so the
          totals are a lower bound.
        </p>
      )}
    </div>
  )
}
