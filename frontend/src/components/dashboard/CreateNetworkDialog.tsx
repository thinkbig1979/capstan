import { useState } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useCreateNetwork } from '@/hooks/useResources'

interface CreateNetworkDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const NAME_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$/

const DRIVERS = [
  { value: 'bridge', label: 'bridge' },
  { value: 'overlay', label: 'overlay' },
  { value: 'macvlan', label: 'macvlan' },
  { value: 'ipvlan', label: 'ipvlan' },
  { value: 'host', label: 'host' },
  { value: 'none', label: 'none' },
]

export function CreateNetworkDialog({ open, onOpenChange }: CreateNetworkDialogProps) {
  const [name, setName] = useState('')
  const [driver, setDriver] = useState('bridge')
  const [internal, setInternal] = useState(false)
  const [attachable, setAttachable] = useState(false)
  const [nameError, setNameError] = useState('')

  const resetForm = () => {
    setName('')
    setDriver('bridge')
    setInternal(false)
    setAttachable(false)
    setNameError('')
  }

  const handleNameChange = (value: string) => {
    setName(value)
    if (!value.trim()) {
      setNameError('Network name is required')
    } else if (!NAME_PATTERN.test(value)) {
      setNameError('Use letters, digits, "_", ".", "-" (must start with a letter or digit, max 63 chars)')
    } else {
      setNameError('')
    }
  }

  const createMutation = useCreateNetwork()

  const handleSubmit = () => {
    handleNameChange(name)
    if (!name.trim() || !NAME_PATTERN.test(name)) return
    createMutation.mutate(
      { name, driver, internal, attachable },
      {
        onSuccess: () => {
          resetForm()
          onOpenChange(false)
        },
      },
    )
  }

  const isSubmitDisabled = !name.trim() || !!nameError || createMutation.isPending

  return (
    <Dialog
      open={open}
      onOpenChange={(isOpen) => {
        if (!isOpen) resetForm()
        onOpenChange(isOpen)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Create Network</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="network-name">Name</Label>
            <Input
              id="network-name"
              value={name}
              onChange={(e) => handleNameChange(e.target.value)}
              placeholder="my-network"
              autoFocus
              aria-invalid={!!nameError}
              aria-describedby={nameError ? 'network-name-error' : undefined}
            />
            {nameError && <p id="network-name-error" className="text-sm text-destructive">{nameError}</p>}
          </div>

          <div className="space-y-2">
            <Label htmlFor="network-driver">Driver</Label>
            <Select value={driver} onValueChange={setDriver}>
              <SelectTrigger id="network-driver">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {DRIVERS.map((d) => (
                  <SelectItem key={d.value} value={d.value}>{d.label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex items-center justify-between gap-4 rounded-md border p-3">
            <div className="space-y-0.5">
              <Label htmlFor="network-internal" className="cursor-pointer">Internal</Label>
              <p className="text-xs text-muted-foreground">Restrict external access to the network.</p>
            </div>
            <Switch id="network-internal" checked={internal} onCheckedChange={setInternal} />
          </div>

          <div className="flex items-center justify-between gap-4 rounded-md border p-3">
            <div className="space-y-0.5">
              <Label htmlFor="network-attachable" className="cursor-pointer">Attachable</Label>
              <p className="text-xs text-muted-foreground">Allow standalone containers to attach (swarm scope).</p>
            </div>
            <Switch id="network-attachable" checked={attachable} onCheckedChange={setAttachable} />
          </div>
        </div>

        <DialogFooter className="mt-2">
          <Button variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button onClick={handleSubmit} disabled={isSubmitDisabled}>
            {createMutation.isPending ? 'Creating...' : 'Create'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
