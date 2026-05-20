import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Lock, Unlock } from 'lucide-react'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'

function formatRemaining(ms: number): string {
  const totalSeconds = Math.max(0, Math.ceil(ms / 1000))
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${minutes}:${seconds.toString().padStart(2, '0')}`
}

export function EnvUnlockStatus() {
  const unlockedUntil = useEnvUnlockStore((s) => s.unlockedUntil)
  const lock = useEnvUnlockStore((s) => s.lock)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (unlockedUntil === null) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [unlockedUntil])

  if (unlockedUntil === null || unlockedUntil <= now) {
    return null
  }

  const remaining = unlockedUntil - now

  return (
    <div className="inline-flex items-center gap-2 rounded-md border border-info/30 bg-info/10 px-3 py-1.5 text-sm">
      <Unlock className="h-4 w-4 text-info" />
      <span className="text-info font-medium">
        Unlocked for {formatRemaining(remaining)}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="h-7 px-2 text-info hover:text-info"
        onClick={lock}
        title="Lock now"
        aria-label="Lock environment variables now"
      >
        <Lock className="h-3.5 w-3.5 mr-1" />
        Lock
      </Button>
    </div>
  )
}
