import { AlertCircle, RotateCcw } from 'lucide-react'
import { Button } from '@/components/ui/button'

interface DisconnectedBannerProps {
  show: boolean
  status: 'connecting' | 'connected' | 'disconnected' | 'reconnecting'
  reconnectAttempts: number
  onReconnect: () => void
}

export function DisconnectedBanner({
  show,
  status,
  reconnectAttempts,
  onReconnect,
}: DisconnectedBannerProps) {
  if (!show) return null

  return (
    <div className="flex items-center justify-between rounded-lg border border-warning/40 bg-warning/10 px-4 py-2 text-warning">
      <div className="flex items-center gap-2">
        <AlertCircle className="h-4 w-4" />
        <span>
          {status === 'reconnecting'
            ? `Connection lost. Reconnecting... (attempt ${reconnectAttempts})`
            : 'Disconnected'}
        </span>
      </div>
      <Button variant="outline" size="sm" onClick={onReconnect}>
        <RotateCcw className="mr-2 h-4 w-4" />
        Reconnect
      </Button>
    </div>
  )
}
