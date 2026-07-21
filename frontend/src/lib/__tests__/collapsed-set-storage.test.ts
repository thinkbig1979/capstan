import { describe, it, expect, beforeEach } from 'vitest'
import { loadCollapsedSet, saveCollapsedSet } from '../collapsed-set-storage'

// Regression: the sidebar and directories-tab "collapsed groups" localStorage
// keys used to be unversioned, so changing the stored shape later would crash
// JSON.parse for existing users. Both now go through this shared, versioned
// helper (":v1" suffix) whose reads migrate once from the legacy key so
// nobody's collapsed state silently resets.
describe('collapsed-set-storage', () => {
  const KEY = 'test-collapsed:v1'
  const LEGACY_KEY = 'test-collapsed'

  beforeEach(() => {
    localStorage.clear()
  })

  it('saves under the versioned key only', () => {
    saveCollapsedSet(KEY, new Set(['/a']))
    expect(localStorage.getItem(KEY)).toBe(JSON.stringify(['/a']))
    expect(localStorage.getItem(LEGACY_KEY)).toBeNull()
  })

  it('reads back what it saved', () => {
    saveCollapsedSet(KEY, new Set(['/a', '/b']))
    expect(loadCollapsedSet(KEY, LEGACY_KEY)).toEqual(new Set(['/a', '/b']))
  })

  it('migrates from the legacy unversioned key when the versioned key is absent', () => {
    localStorage.setItem(LEGACY_KEY, JSON.stringify(['/legacy']))
    expect(loadCollapsedSet(KEY, LEGACY_KEY)).toEqual(new Set(['/legacy']))
  })

  it('prefers the versioned key over the legacy key once both exist', () => {
    localStorage.setItem(LEGACY_KEY, JSON.stringify(['/legacy']))
    localStorage.setItem(KEY, JSON.stringify(['/current']))
    expect(loadCollapsedSet(KEY, LEGACY_KEY)).toEqual(new Set(['/current']))
  })

  it('returns null when neither key has data', () => {
    expect(loadCollapsedSet(KEY, LEGACY_KEY)).toBeNull()
  })

  it('does not crash on malformed stored JSON, and does not fall back to the legacy key', () => {
    localStorage.setItem(LEGACY_KEY, JSON.stringify(['/legacy']))
    localStorage.setItem(KEY, '{not valid json')
    expect(loadCollapsedSet(KEY, LEGACY_KEY)).toBeNull()
  })
})
