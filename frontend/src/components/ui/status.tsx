import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

export type StatusTone = "success" | "warning" | "error" | "info" | "neutral"

const statusBadgeVariants = cva(
  "inline-flex w-fit shrink-0 items-center gap-1 overflow-hidden rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap",
  {
    variants: {
      tone: {
        success: "bg-success/15 text-success border-success/30",
        warning: "bg-warning/15 text-warning border-warning/30",
        error: "bg-destructive/15 text-destructive border-destructive/30",
        info: "bg-info/15 text-info border-info/30",
        neutral: "bg-muted text-muted-foreground border-border",
      },
    },
    defaultVariants: {
      tone: "neutral",
    },
  },
)

const toneText: Record<StatusTone, string> = {
  success: "text-success",
  warning: "text-warning",
  error: "text-destructive",
  info: "text-info",
  neutral: "text-muted-foreground",
}

const toneBg: Record<StatusTone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  error: "bg-destructive",
  info: "bg-info",
  neutral: "bg-muted",
}

const toneBorder: Record<StatusTone, string> = {
  success: "border-success/30",
  warning: "border-warning/30",
  error: "border-destructive/30",
  info: "border-info/30",
  neutral: "border-border",
}

const toneBgSubtle: Record<StatusTone, string> = {
  success: "bg-success/10",
  warning: "bg-warning/10",
  error: "bg-destructive/10",
  info: "bg-info/10",
  neutral: "bg-muted/50",
}

export function statusTextClass(tone: StatusTone): string {
  return toneText[tone]
}

export function statusBgClass(tone: StatusTone): string {
  return toneBg[tone]
}

export function statusBorderClass(tone: StatusTone): string {
  return toneBorder[tone]
}

export function statusBgSubtleClass(tone: StatusTone): string {
  return toneBgSubtle[tone]
}

export interface StatusProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof statusBadgeVariants> {
  tone: StatusTone
}

export function Status({ tone, className, children, ...props }: StatusProps) {
  return (
    <span
      data-slot="status"
      data-tone={tone}
      className={cn(statusBadgeVariants({ tone }), className)}
      {...props}
    >
      {children}
    </span>
  )
}

export function StatusDot({
  tone,
  pulse = false,
  className,
}: {
  tone: StatusTone
  pulse?: boolean
  className?: string
}) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-block h-2 w-2 rounded-full",
        toneBg[tone],
        pulse && "animate-pulse",
        className,
      )}
    />
  )
}

export { statusBadgeVariants }
