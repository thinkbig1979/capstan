/**
 * A global value the page cannot write back as KEY=value, shown read-only:
 * one spanning several lines (agent-os-fw2q, agent-os-rd1l), which a one-line
 * input would flatten, and every other line the server marks readOnly (a bare
 * KEY, KEY: value, a second variable on its line; agent-os-jb5g). The server
 * refuses a change to either. The global page has no Raw view: the value is
 * changed by editing global.env directly.
 */
export function ReadOnlyValue({ value, masked }: { value: string; masked: boolean }) {
  const multiline = value.includes('\n')
  return (
    <div className="min-w-0 flex-1 space-y-1">
      <span className="block whitespace-pre-wrap break-all text-sm text-muted-foreground">
        {masked ? '••••••' : value}
      </span>
      <span className="text-xs text-muted-foreground">
        {multiline ? 'Multi-line value, read-only here' : 'Read-only here, edit global.env directly'}
      </span>
    </div>
  )
}
