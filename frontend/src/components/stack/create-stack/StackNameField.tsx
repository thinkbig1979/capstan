import type { RefObject } from 'react'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

interface StackNameFieldProps {
  name: string
  nameError: string
  onNameChange: (value: string) => void
  directoryPreview: string
  nameInputRef: RefObject<HTMLInputElement | null>
}

export function StackNameField({
  name,
  nameError,
  onNameChange,
  directoryPreview,
  nameInputRef,
}: StackNameFieldProps) {
  return (
    <div className="space-y-2">
      <Label htmlFor="name">Stack Name</Label>
      <Input
        id="name"
        ref={nameInputRef}
        value={name}
        onChange={(e) => onNameChange(e.target.value)}
        placeholder="my-stack"
        aria-invalid={!!nameError}
        aria-describedby={nameError ? 'stack-name-error' : undefined}
      />
      {nameError && <p id="stack-name-error" className="text-sm text-destructive">{nameError}</p>}
      {name && !nameError && (
        <p className="text-xs text-muted-foreground">
          Directory will be {directoryPreview}/{name}
        </p>
      )}
    </div>
  )
}
