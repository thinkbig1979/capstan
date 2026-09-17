// Versioned localStorage helper for persisting a Set<string> of "collapsed"
// group keys (sidebar tree groups, directories tab groups, etc). Reads
// migrate once from an unversioned legacy key so existing users don't lose
// their collapsed state now that the key carries a version suffix — see
// https://react.doctor/docs/rules/react-doctor/client-localstorage-no-version.
import { stringArrayOr } from './narrow'

export function loadCollapsedSet(key: string, legacyKey: string): Set<string> | null {
  try {
    // agent-os-06c1: JSON.parse returns `any`, and localStorage is editable
    // by anyone at the keyboard, so `new Set(JSON.parse(raw))` would hand back
    // a Set of arbitrary values declared as Set<string>. The consequence here
    // is a silent .has() miss rather than bad copy, but the shape is the same.
    const raw = localStorage.getItem(key)
    if (raw) return new Set(stringArrayOr(JSON.parse(raw), []))
    const legacy = localStorage.getItem(legacyKey)
    if (legacy) return new Set(stringArrayOr(JSON.parse(legacy), []))
  } catch {
    /* ignore */
  }
  return null
}

export function saveCollapsedSet(key: string, set: Set<string>) {
  localStorage.setItem(key, JSON.stringify([...set]))
}
