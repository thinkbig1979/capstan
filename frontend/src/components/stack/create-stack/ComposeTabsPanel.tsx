import type { RefObject } from 'react'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { FileCheck, Terminal } from 'lucide-react'
import type { LintResult } from '@/types'

interface ComposeTabsPanelProps {
  composeRef: RefObject<HTMLDivElement | null>
  composeTab: 'editor' | 'docker-run'
  onComposeTabChange: (tab: 'editor' | 'docker-run') => void
  onLint: () => void
  editorRef: RefObject<HTMLDivElement | null>
  hasLintErrors: boolean
  lintResults: LintResult[]
  dockerRunInput: string
  onDockerRunInputChange: (value: string) => void
  conversionError: string
  onConvert: () => void
}

export function ComposeTabsPanel({
  composeRef,
  composeTab,
  onComposeTabChange,
  onLint,
  editorRef,
  hasLintErrors,
  lintResults,
  dockerRunInput,
  onDockerRunInputChange,
  conversionError,
  onConvert,
}: ComposeTabsPanelProps) {
  return (
    <div className="space-y-2" ref={composeRef}>
      <div className="flex items-center justify-between">
        <Label>Docker Compose</Label>
        <Button variant="outline" size="sm" onClick={onLint}>
          <FileCheck className="mr-2 h-4 w-4" />
          Lint
        </Button>
      </div>

      <div className="inline-flex h-9 items-center justify-center rounded-lg bg-muted p-1 text-muted-foreground w-full">
        <button
          type="button"
          onClick={() => onComposeTabChange('editor')}
          className={`inline-flex items-center justify-center whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium ring-offset-background transition-all flex-1 ${
            composeTab === 'editor'
              ? 'bg-background text-foreground shadow'
              : 'hover:bg-background/50'
          }`}
        >
          Compose Editor
        </button>
        <button
          type="button"
          onClick={() => onComposeTabChange('docker-run')}
          className={`inline-flex items-center justify-center whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium ring-offset-background transition-all flex-1 ${
            composeTab === 'docker-run'
              ? 'bg-background text-foreground shadow'
              : 'hover:bg-background/50'
          }`}
        >
          <Terminal className="mr-2 h-3 w-3" />
          Convert docker run
        </button>
      </div>

      {composeTab === 'editor' && (
        <div className="space-y-2">
          <div
            ref={editorRef}
            className={`rounded-md border overflow-hidden ${hasLintErrors ? 'border-destructive' : ''}`}
            style={{ minHeight: '300px' }}
          />
          {hasLintErrors && (
            <p className="text-sm text-destructive">
              {lintResults.filter((r) => r.level === 'error').length} error(s) found - fix before creating
            </p>
          )}
        </div>
      )}

      {composeTab === 'docker-run' && (
        <div className="space-y-3">
          <Textarea
            value={dockerRunInput}
            onChange={(e) => onDockerRunInputChange(e.target.value)}
            placeholder={`docker run -d \\\n  --name webapp \\\n  -p 8080:80 \\\n  -e NODE_ENV=production \\\n  nginx:alpine`}
            className="font-mono min-h-[200px] text-sm"
          />
          {conversionError && (
            <p className="text-sm text-destructive">{conversionError}</p>
          )}
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">
              Paste a <code className="bg-muted px-1 rounded">docker run</code> command to convert it to Compose format
            </p>
            <Button
              variant="secondary"
              size="sm"
              onClick={onConvert}
              disabled={!dockerRunInput.trim()}
            >
              <Terminal className="mr-2 h-4 w-4" />
              Convert
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
