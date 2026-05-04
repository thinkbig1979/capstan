import { Badge } from '@/components/ui/badge'
import { AlertCircle, AlertTriangle, Info } from 'lucide-react'
import type { LintResult } from '@/types'

export function getLintIcon(level: string) {
  switch (level) {
    case 'error':
      return <AlertCircle className="h-4 w-4 text-red-500" />
    case 'warning':
      return <AlertTriangle className="h-4 w-4 text-yellow-500" />
    default:
      return <Info className="h-4 w-4 text-blue-500" />
  }
}

interface LintResultsPanelProps {
  results: LintResult[]
  maxHeight?: string
}

export function LintResultsPanel({ results, maxHeight }: LintResultsPanelProps) {
  if (results.length === 0) return null

  return (
    <div className="rounded-md border bg-card">
      <div className="p-4 border-b">
        <h3 className="font-semibold">Lint Results</h3>
      </div>
      <div
        className="divide-y"
        style={maxHeight ? { maxHeight, overflowY: 'auto' } : undefined}
      >
        {results.map((result, index) => (
          <div
            key={`${result.level}-${result.line || index}-${result.message}`}
            className="p-3 flex items-start gap-3 hover:bg-muted/50"
          >
            {getLintIcon(result.level)}
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2">
                {result.line && (
                  <Badge variant="outline" className="text-xs">
                    Line {result.line}
                  </Badge>
                )}
                <span className="text-sm font-medium">{result.message}</span>
              </div>
              {result.rule && (
                <div className="text-xs text-muted-foreground mt-1">
                  {result.rule}
                </div>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
