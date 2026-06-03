import { useEffect, useState, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { LayoutDashboard, Layers, Settings } from 'lucide-react'
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

export function CommandPalette() {
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()

  const { data: stacks = [] } = useQuery({
    queryKey: ['stacks'],
    queryFn: stacksApi.list,
    staleTime: 30_000,
  })

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
        <CommandEmpty>No results found.</CommandEmpty>

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
