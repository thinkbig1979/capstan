import { useState, useMemo } from 'react'
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { settingsApi, directoryConfigApi } from '@/lib/api'
import { TableSearch } from '@/components/ui/table-search'
import { useTextFilter } from '@/hooks/useTextFilter'
import { toast } from 'sonner'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { queryKeys } from '@/lib/query-keys'

// Directories are plain strings; match against the full path and the basename.
const DIR_SEARCH_FIELDS = [
  (dir: string) => dir,
  (dir: string) => dir.split('/').filter(Boolean).pop() ?? dir,
]

export function DirectoriesSettingsContent() {
  const { data: config, isLoading } = useQuery({
    queryKey: queryKeys.config(),
    queryFn: () => settingsApi.getConfig(),
  })
  const { data: scanDepthData, isLoading: isLoadingDepth } = useQuery({
    queryKey: queryKeys.scanDepth(),
    queryFn: () => settingsApi.getScanDepth(),
  })
  const queryClient = useQueryClient()
  const [defaultDir, setDefaultDir] = useState('')
  const [scanDepth, setScanDepth] = useState('1')
  const [initialized, setInitialized] = useState(false)
  const [depthInitialized, setDepthInitialized] = useState(false)

  const scanDepthMutation = useMutation({
    mutationFn: (depth: number) => settingsApi.updateScanDepth(depth),
    onSuccess: () => {
      toast.success('Scan depth updated. Rescan directories to discover nested stacks.')
      queryClient.invalidateQueries({ queryKey: queryKeys.scanDepth() })
    },
    onError: () => toast.error('Failed to update scan depth'),
  })

  // Hydrate local editable state from the query results once they load.
  // Adjusted during render (rather than in an effect) — the `initialized`
  // guards make this a one-shot assignment, not an unbounded render loop.
  if (config && !initialized) {
    setInitialized(true)
    setDefaultDir(config.stacksDir || '')
  }

  if (scanDepthData && !depthInitialized) {
    setDepthInitialized(true)
    setScanDepth(String(scanDepthData.scanDepth))
  }

  const allDirs = useMemo(() => config?.stacksDirectories ?? [], [config])
  const { query, setQuery, filtered: filteredDirs } = useTextFilter(allDirs, DIR_SEARCH_FIELDS)

  if (isLoading || isLoadingDepth) {
    return <div className="py-4"><LoadingSpinner /></div>
  }

  const effectiveDefault = initialized ? defaultDir : (config?.stacksDir || '')
  const effectiveDepth = depthInitialized ? scanDepth : String(scanDepthData?.scanDepth || 1)

  const handleSaveDefault = () => {
    directoryConfigApi.update({ defaultDir: effectiveDefault }).then(() => {
      toast.success('Default directory updated')
      queryClient.invalidateQueries({ queryKey: queryKeys.config() })
    }).catch(() => {
      toast.error('Failed to update default directory')
    })
  }

  return (
    <div className="space-y-6">
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <h3 className="text-lg font-medium">Monitored Directories</h3>
          {allDirs.length > 1 && (
            <TableSearch
              value={query}
              onChange={setQuery}
              placeholder="Filter directories…"
              className="w-full sm:w-56"
            />
          )}
        </div>
        {allDirs.length > 0 ? (
          filteredDirs.length > 0 ? (
            <div className="space-y-2">
              {filteredDirs.map((dir: string) => {
                const isDefault = dir === allDirs[0]
                return (
                  <div key={dir} className="flex items-center gap-3 p-3 rounded-md border bg-muted/30">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium truncate">{dir.split('/').filter(Boolean).pop() || dir}</span>
                        {isDefault && (
                          <Badge variant="secondary" className="text-[10px] px-1.5 py-0">Default</Badge>
                        )}
                      </div>
                      <p className="text-xs text-muted-foreground truncate">{dir}</p>
                    </div>
                  </div>
                )
              })}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No directories match &quot;{query}&quot;.</p>
          )
        ) : (
          <p className="text-sm text-muted-foreground">No directories configured</p>
        )}
        <p className="text-xs text-muted-foreground">
          Additional directories can be added via the EXTRA_STACKS_DIRS environment variable (comma-separated paths).
        </p>
      </div>

      <div className="space-y-4 pt-4 border-t">
        <h3 className="text-lg font-medium">Scan Depth</h3>
        <div className="space-y-2">
          <Label htmlFor="scan-depth">Directory Recursion Depth</Label>
          <Select value={effectiveDepth} onValueChange={setScanDepth}>
            <SelectTrigger id="scan-depth" className="w-full max-w-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {Array.from({ length: 10 }, (_, i) => i + 1).map((d) => (
                <SelectItem key={d} value={String(d)}>
                  {d} level{d > 1 ? 's' : ''} deep
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">
            How many levels deep to scan within each monitored directory for compose files. A value of 1 only scans immediate subdirectories. After changing this, trigger a rescan to discover newly visible stacks.
          </p>
        </div>
        <div className="flex justify-end">
          <Button
            onClick={() => scanDepthMutation.mutate(Number(effectiveDepth))}
            disabled={effectiveDepth === String(scanDepthData?.scanDepth) || scanDepthMutation.isPending}
          >
            {scanDepthMutation.isPending ? <LoadingSpinner size="small" /> : 'Save Scan Depth'}
          </Button>
        </div>
      </div>

      {allDirs.length > 1 && (
        <div className="space-y-4 pt-4 border-t">
          <h3 className="text-lg font-medium">Default Stack Directory</h3>
          <div className="space-y-2">
            <Label htmlFor="default-dir">Default Directory for New Stacks</Label>
            <Select value={effectiveDefault} onValueChange={setDefaultDir}>
              <SelectTrigger id="default-dir" className="w-full max-w-md">
                <SelectValue placeholder="Select default directory" />
              </SelectTrigger>
              <SelectContent>
                {allDirs.map((dir: string) => (
                  <SelectItem key={dir} value={dir}>
                    {dir.split('/').filter(Boolean).pop() || dir} ({dir})
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              New stacks will be created in this directory by default unless changed in the creation dialog.
            </p>
          </div>
          <div className="flex justify-end">
            <Button onClick={handleSaveDefault} disabled={effectiveDefault === config?.stacksDir}>
              Save Default Directory
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
