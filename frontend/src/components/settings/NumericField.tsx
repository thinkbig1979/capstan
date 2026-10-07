import { useId } from 'react'
import { Input } from '@/components/ui/input'
import { parseWholeNumber } from './parseWholeNumber'

interface NumericFieldProps
  extends Omit<React.ComponentProps<typeof Input>, 'type' | 'value' | 'onChange'> {
  /** null = the field is empty. The caller must treat it as invalid, not as 0. */
  value: number | null
  onValueChange: (value: number | null) => void
}

/**
 * A number input that keeps an empty box empty and says so. The caller holds
 * `number | null` and refuses to save while it is null; a typed "0" stays a real
 * 0, so help text like "0 disables scheduled backups" keeps working.
 */
export function NumericField({ value, onValueChange, className, ...props }: NumericFieldProps) {
  const messageId = useId()
  const empty = value === null
  return (
    <>
      <Input
        {...props}
        type="number"
        value={value ?? ''}
        onChange={(e) => onValueChange(parseWholeNumber(e.target.value))}
        aria-invalid={empty || undefined}
        aria-describedby={empty ? messageId : props['aria-describedby']}
        className={className}
      />
      {empty && (
        <p id={messageId} className="text-xs text-destructive">
          Enter a number
        </p>
      )}
    </>
  )
}
