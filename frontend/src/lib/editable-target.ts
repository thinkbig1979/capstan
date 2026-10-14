/**
 * True when a key event's target takes text input, so a single-key shortcut
 * (`/` in the log viewer, `?` for help) must leave the keystroke alone.
 * CodeMirror's content is contentEditable and xterm's input is a textarea.
 */
export function isEditableTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}
