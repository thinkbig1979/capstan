import { useState, type Dispatch, type SetStateAction } from 'react'
import type { EnvEntry } from '@/types'
import { isSensitiveKey } from './sensitiveKey'

/**
 * When the unlock session ends (manual lock or auto-expiry), re-mask any
 * sensitive-by-name entries the user had revealed during the session.
 * Adjusted during render (rather than in an effect) by comparing against
 * the previous render's unlockedUntil — see
 * https://react.dev/learn/you-might-not-need-an-effect.
 *
 * Must be called unconditionally, before any early return in the caller, so
 * this comparison runs on every render (store-driven transitions — manual
 * lock or the store's own expiry timer — reach it via a normal re-render;
 * a remounted caller simply starts its own local `prevUnlockedUntil` fresh).
 */
export function useEnvUnlockRemask(
  unlockedUntil: number | null,
  setEntries: Dispatch<SetStateAction<EnvEntry[]>>,
) {
  const [prevUnlockedUntil, setPrevUnlockedUntil] = useState(unlockedUntil)
  if (unlockedUntil !== prevUnlockedUntil) {
    setPrevUnlockedUntil(unlockedUntil)
    if (unlockedUntil === null) {
      setEntries((prev) => {
        let changed = false
        const next = prev.map((e) => {
          if (!e.sensitive && isSensitiveKey(e.key)) {
            changed = true
            return { ...e, sensitive: true }
          }
          return e
        })
        return changed ? next : prev
      })
    }
  }
}
