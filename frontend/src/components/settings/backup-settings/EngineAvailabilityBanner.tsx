import { AlertCircle } from 'lucide-react'

interface EngineAvailabilityBannerProps {
  resticAvailable: boolean
  rcloneAvailable: boolean
}

export function EngineAvailabilityBanner({ resticAvailable, rcloneAvailable }: EngineAvailabilityBannerProps) {
  if (resticAvailable && rcloneAvailable) return null

  return (
    <div className="flex items-start gap-3 rounded-lg border border-destructive/30 bg-destructive/10 p-4">
      <AlertCircle className="h-5 w-5 text-destructive shrink-0 mt-0.5" />
      <div className="space-y-1">
        {!resticAvailable && (
          <p className="text-sm font-medium text-destructive">
            restic binary not found — backups are disabled
          </p>
        )}
        {!rcloneAvailable && (
          <p className="text-sm font-medium text-destructive">
            rclone binary not found — cloud sync is disabled
          </p>
        )}
        <p className="text-xs text-destructive/80">
          Install the missing binaries inside the container and restart Capstan.
        </p>
      </div>
    </div>
  )
}
