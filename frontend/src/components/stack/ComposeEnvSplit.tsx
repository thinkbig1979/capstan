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
  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: `compose-env-split-${orientation}`,
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
      <ResizablePanel defaultSize={50} minSize={25} className="pr-0 md:pr-3">
        <div className="h-full overflow-auto">
          <TabErrorBoundary>
            <Suspense fallback={<EditorSkeleton />}>
              <ComposeEditor stackId={stackId} />
            </Suspense>
          </TabErrorBoundary>
        </div>
      </ResizablePanel>
      <ResizableHandle withHandle className="my-3 md:my-0 md:mx-1" />
      <ResizablePanel defaultSize={50} minSize={25} className="pl-0 md:pl-3">
        <div className="h-full overflow-auto">
          <TabErrorBoundary>
            <EnvEditor stackId={stackId} />
          </TabErrorBoundary>
        </div>
      </ResizablePanel>
    </ResizablePanelGroup>
  )
}
