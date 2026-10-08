import { useEffect, useState, useCallback } from 'react'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { LayoutDashboard, Layers, Loader2, Settings } from 'lucide-react'
import { stacksApi } from '@/lib/api'
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command'
import { queryKeys } from '@/lib/query-keys'
import { LoadFailedNotice } from '@/components/LoadFailedNotice'

export function CommandPalette() {
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()

  const { data, isPending, isError, refetch } = useQuery({
    queryKey: queryKeys.stacks(),
    queryFn: stacksApi.list,
  })
  const stacks = data ?? []
  // Without this a failed load leaves only "No results found." for a stack
  // search, as if the stack did not exist (agent-os-kdqm).
  const stacksLoadFailed = isError && !data
  // No `enabled` on this query, so plain isPending is the whole answer: it is
  // true while the first fetch runs AND while it is paused offline, which
  // isLoading is not (agent-os-hzjh, agent-os-7nqa).
  const stacksLoading = isPending

  const handleClose = useCallback(() => setOpen(false), [])

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        // Always open the palette on Ctrl-K / Cmd-K, even from inputs
        e.preventDefault()
        setOpen((prev) => !prev)
      }
    }

    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [])

  const runCommand = useCallback(
    (fn: () => void) => {
      handleClose()
      fn()
    },
    [handleClose],
  )

  return (
    <CommandDialog open={open} onOpenChange={setOpen}>
      <CommandInput placeholder="Search stacks, navigate..." />
      <CommandList>
        {stacksLoadFailed && (
          <LoadFailedNotice what="the stack list" onRetry={() => void refetch()} className="m-2" />
        )}
        {stacksLoading ? (
          // "No results found." would claim the stack does not exist when the
          // list has not been read yet, so it is not rendered until it has.
          <div role="status" className="flex items-center justify-center gap-2 py-6 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
            Loading stacks...
          </div>
        ) : (
          <CommandEmpty>No results found.</CommandEmpty>
        )}

        {stacks.length > 0 && (
          <>
            <CommandGroup heading="Stacks">
              {stacks.map((stack) => (
                <CommandItem
                  key={stack.id}
                  value={stack.projectName}
                  onSelect={() => runCommand(() => navigate(`/stacks/${stack.id}`))}
                >
                  <Layers className="mr-2 h-4 w-4 shrink-0 text-muted-foreground" />
                  {stack.projectName}
                </CommandItem>
              ))}
            </CommandGroup>
            <CommandSeparator />
          </>
        )}

        <CommandGroup heading="Navigation">
          <CommandItem
            value="Dashboard home"
            onSelect={() => runCommand(() => navigate('/'))}
          >
            <LayoutDashboard className="mr-2 h-4 w-4 shrink-0 text-muted-foreground" />
            Dashboard
          </CommandItem>
          <CommandItem
            value="Settings"
            onSelect={() => runCommand(() => navigate('/settings'))}
          >
            <Settings className="mr-2 h-4 w-4 shrink-0 text-muted-foreground" />
            Settings
          </CommandItem>
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  )
}
