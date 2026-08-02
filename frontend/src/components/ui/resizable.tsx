import { GripVerticalIcon } from 'lucide-react'
import { Group, Panel, Separator } from 'react-resizable-panels'

import { cn } from '@/lib/utils'

function ResizablePanelGroup({
  className,
  orientation = 'horizontal',
  ...props
}: React.ComponentProps<typeof Group>) {
  return (
    <Group
      data-slot="resizable-panel-group"
      // v4 no longer renders a `data-panel-group-direction`-style attribute on
      // the group element (orientation only drives an inline flex-direction
      // style), so we render it ourselves. This keeps the vertical-stack
      // Tailwind class working and preserves the attribute existing
      // tests/consumers select on.
      data-panel-group-direction={orientation}
      orientation={orientation}
      className={cn(
        'flex h-full w-full',
        orientation === 'vertical' && 'flex-col',
        className
      )}
      {...props}
    />
  )
}

function ResizablePanel({
  ...props
}: React.ComponentProps<typeof Panel>) {
  return <Panel data-slot="resizable-panel" {...props} />
}

function ResizableHandle({
  withHandle,
  className,
  ...props
}: React.ComponentProps<typeof Separator> & {
  withHandle?: boolean
}) {
  return (
    <Separator
      data-slot="resizable-handle"
      // v4's Separator sets `aria-orientation` to the orientation of the
      // divider line itself, which is the inverse of the parent Group's
      // `orientation` (a horizontal Group — panels side by side — has a
      // vertical divider, and vice versa). That's exactly the same case the
      // old `data-panel-group-direction=vertical` selectors targeted, so we
      // swap to `aria-[orientation=horizontal]` in its place.
      className={cn(
        'bg-border focus-visible:ring-ring relative flex w-px items-center justify-center after:absolute after:inset-y-0 after:left-1/2 after:w-1 after:-translate-x-1/2 focus-visible:ring-1 focus-visible:ring-offset-1 focus-visible:outline-hidden aria-[orientation=horizontal]:h-px aria-[orientation=horizontal]:w-full aria-[orientation=horizontal]:after:left-0 aria-[orientation=horizontal]:after:h-1 aria-[orientation=horizontal]:after:w-full aria-[orientation=horizontal]:after:-translate-y-1/2 aria-[orientation=horizontal]:after:translate-x-0 [&[aria-orientation=horizontal]>div]:rotate-90',
        className
      )}
      {...props}
    >
      {withHandle && (
        <div className="bg-border z-10 flex h-4 w-3 items-center justify-center rounded-xs border">
          <GripVerticalIcon className="size-2.5" />
        </div>
      )}
    </Separator>
  )
}

export { ResizablePanelGroup, ResizablePanel, ResizableHandle }
