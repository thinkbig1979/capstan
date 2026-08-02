import { Suspense, lazy } from 'react'
import { useDefaultLayout } from 'react-resizable-panels'
import { EnvEditor } from './EnvEditor'
import { TabErrorBoundary } from '@/components/TabErrorBoundary'
import {
  ResizablePanelGroup,
  ResizablePanel,
  ResizableHandle,
} from '@/components/ui/resizable'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { EditorSkeleton } from '@/components/LoadingSkeleton'

// Lazy: codemirror only loads once a Compose or Compose+Env split tab is
// actually opened, instead of shipping with every stack detail page visit.
const ComposeEditor = lazy(() =>
  import('./ComposeEditor').then((m) => ({ default: m.ComposeEditor })),
)

interface ComposeEnvSplitProps {
  stackId: string
}

/**
 * Side-by-side Compose + Environment editors with a draggable divider.
 *
 * Both editors are self-contained (they each own their state, save/lint logic,
 * undo/redo, and validation keyed only off stackId), so rendering them in two
 * panes preserves every feature of the standalone tabs with no extra wiring.
 *
 * Layout: horizontal panes on md+ screens, stacked vertically below that so each
 * editor stays usable on narrow viewports. The split ratio is persisted per
 * orientation via autoSaveId.
 */
export function ComposeEnvSplit({ stackId }: ComposeEnvSplitProps) {
  const isWide = useMediaQuery('(min-width: 768px)')
  const orientation = isWide ? 'horizontal' : 'vertical'
  // react-resizable-panels v4 dropped `autoSaveId`; `useDefaultLayout` is its
  // replacement for persisting/restoring a group's layout via storage.
  // Passing panelIds makes the storage key `react-resizable-panels:<id>:compose:env`
  // (keyed by these stable, human-readable panel names) instead of v4's
  // fallback heuristic, which infers panel identity from a single stored key
  // by splitting on commas -- fragile, and unnecessary once ids are explicit.
  // This key does NOT match v3's old `react-resizable-panels:<autoSaveId>`
  // key, so any split ratio a user previously saved is abandoned (old data is
  // simply never read) and they see one silent reset to the 50/50 default the
  // first time they open this after the upgrade -- verified in a real
  // browser: dragging then reloading restores the new layout correctly, and
  // seeding localStorage with old-format v3 data does not error, it's just
  // ignored and the panels render at defaultSize.
  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: `compose-env-split-${orientation}`,
    panelIds: ['compose', 'env'],
    storage: window.localStorage,
  })

  return (
    <ResizablePanelGroup
      // Key by orientation so the panels remount cleanly when it flips, and
      // persist each orientation's ratio independently.
      key={orientation}
      orientation={orientation}
      defaultLayout={defaultLayout}
      onLayoutChanged={onLayoutChanged}
      className="min-h-[600px] rounded-lg"
    >
      <ResizablePanel id="compose" defaultSize={50} minSize={25} className="pr-0 md:pr-3">
        <div className="h-full overflow-auto">
          <TabErrorBoundary>
            <Suspense fallback={<EditorSkeleton />}>
              <ComposeEditor stackId={stackId} />
            </Suspense>
          </TabErrorBoundary>
        </div>
      </ResizablePanel>
      <ResizableHandle withHandle className="my-3 md:my-0 md:mx-1" />
      <ResizablePanel id="env" defaultSize={50} minSize={25} className="pl-0 md:pl-3">
        <div className="h-full overflow-auto">
          <TabErrorBoundary>
            <EnvEditor stackId={stackId} />
          </TabErrorBoundary>
        </div>
      </ResizablePanel>
    </ResizablePanelGroup>
  )
}
