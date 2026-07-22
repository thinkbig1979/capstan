import { Badge } from '@/components/ui/badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { UpdateLogTab } from '@/components/dashboard/UpdateLogTab'
import { useUpdatesData } from './updates-tab/useUpdatesData'
import { AvailableUpdatesPanel } from './updates-tab/AvailableUpdatesPanel'

export function UpdatesTab() {
  const data = useUpdatesData()
  const { hasData, updates } = data

  return (
    <Tabs defaultValue="available">
      <TabsList>
        <TabsTrigger value="available" className="gap-1.5">
          Available Updates
          {hasData && (
            <Badge variant="destructive" className="ml-1 h-5 min-w-[20px] text-[10px] px-1">
              {updates.length}
            </Badge>
          )}
        </TabsTrigger>
        <TabsTrigger value="log">
          Update Log
        </TabsTrigger>
      </TabsList>

      <TabsContent value="available" className="mt-4">
        <AvailableUpdatesPanel data={data} />
      </TabsContent>

      <TabsContent value="log" className="mt-4">
        <UpdateLogTab />
      </TabsContent>
    </Tabs>
  )
}
