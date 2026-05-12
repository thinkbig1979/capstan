import type React from 'react'
import { TabsList, TabsTrigger, type tabsListVariants } from '@/components/ui/tabs'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { cn } from '@/lib/utils'
import type { VariantProps } from 'class-variance-authority'

interface Tab {
  value: string
  label: React.ReactNode
}

interface ResponsiveTabsListProps {
  tabs: Tab[]
  value: string
  onValueChange: (value: string) => void
  variant?: VariantProps<typeof tabsListVariants>['variant']
  className?: string
}

export function ResponsiveTabsList({
  tabs,
  value,
  onValueChange,
  variant = 'default',
  className,
}: ResponsiveTabsListProps) {
  return (
    <>
      <div className="flex md:hidden">
        <Select value={value} onValueChange={onValueChange}>
          <SelectTrigger className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {tabs.map((tab) => (
              <SelectItem key={tab.value} value={tab.value}>
                {tab.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <TabsList variant={variant} className={cn('hidden md:inline-flex w-full', className)}>
        {tabs.map((tab) => (
          <TabsTrigger key={tab.value} value={tab.value}>
            {tab.label}
          </TabsTrigger>
        ))}
      </TabsList>
    </>
  )
}
