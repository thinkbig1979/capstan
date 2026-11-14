/**
 * A global value spanning several lines (agent-os-fw2q, agent-os-rd1l). A
 * one-line input would flatten it, and the server refuses a change to it, so
 * the table and the cards show it read-only. The global page has no Raw view:
 * the value is changed by editing global.env directly.
 */
export function MultilineValue({ value, masked }: { value: string; masked: boolean }) {
  return (
    <div className="min-w-0 flex-1 space-y-1">
      <span className="block whitespace-pre-wrap break-all text-sm text-muted-foreground">
        {masked ? '••••••' : value}
      </span>
      <span className="text-xs text-muted-foreground">Multi-line value, read-only here</span>
    </div>
  )
}
