import * as React from "react"
import {
  AlertTriangle,
  BellOff,
  BellRing,
  CheckCircle2,
  CircleDashed,
  Loader2,
  XCircle,
  type LucideIcon,
} from "lucide-react"
import { cn } from "@/lib/utils"

/** Status is always icon + word; color only supports. */
export function StatusLabel({
  icon: Icon,
  tone,
  children,
  className,
  spin,
}: {
  icon: LucideIcon
  tone: "good" | "warning" | "critical" | "muted"
  children: React.ReactNode
  className?: string
  spin?: boolean
}) {
  const color = {
    good: "text-good",
    warning: "text-warning",
    critical: "text-critical",
    muted: "text-muted-foreground",
  }[tone]
  return (
    <span className={cn("inline-flex items-center gap-1.5 font-medium", color, className)}>
      <Icon className={cn("size-3.5 shrink-0", spin && "animate-spin")} strokeWidth={1.75} aria-hidden />
      {children}
    </span>
  )
}

/** Proxy status: active / failed / idle. */
export function ProxyStatus({ status, className }: { status: string; className?: string }) {
  switch (status) {
    case "active":
      return <StatusLabel icon={CheckCircle2} tone="good" className={className}>Active</StatusLabel>
    case "failed":
      return <StatusLabel icon={XCircle} tone="critical" className={className}>Failed</StatusLabel>
    case "idle":
      return <StatusLabel icon={CircleDashed} tone="muted" className={className}>Idle</StatusLabel>
    default:
      return <StatusLabel icon={CircleDashed} tone="muted" className={className}>{status}</StatusLabel>
  }
}

export function OkStatus({ children = "OK" }: { children?: React.ReactNode }) {
  return <StatusLabel icon={CheckCircle2} tone="good">{children}</StatusLabel>
}
export function ErrorStatus({ children = "Error" }: { children?: React.ReactNode }) {
  return <StatusLabel icon={XCircle} tone="critical">{children}</StatusLabel>
}
export function WarnStatus({ children }: { children: React.ReactNode }) {
  return <StatusLabel icon={AlertTriangle} tone="warning">{children}</StatusLabel>
}
export function PendingStatus({ children = "Pending" }: { children?: React.ReactNode }) {
  return <StatusLabel icon={CircleDashed} tone="muted">{children}</StatusLabel>
}
export function RunningStatus({ children = "Running" }: { children?: React.ReactNode }) {
  return <StatusLabel icon={Loader2} tone="muted" spin>{children}</StatusLabel>
}
/** Off is a working feature, not an error. */
export function OnOff({ on, onLabel = "On", offLabel = "Off" }: { on: boolean; onLabel?: string; offLabel?: string }) {
  return on
    ? <StatusLabel icon={BellRing} tone="good">{onLabel}</StatusLabel>
    : <StatusLabel icon={BellOff} tone="muted">{offLabel}</StatusLabel>
}

/** Small bordered badge. `strong` for the one that stands out. */
export function Tag({
  children,
  strong,
  mono,
  className,
  title,
}: {
  children: React.ReactNode
  strong?: boolean
  mono?: boolean
  className?: string
  title?: string
}) {
  return (
    <span
      title={title}
      className={cn(
        "inline-flex items-center rounded border px-1.5 py-0.5 text-[0.6875rem] leading-4 font-medium whitespace-nowrap",
        strong ? "border-primary/25 bg-primary/8 text-foreground" : "border-border text-muted-foreground",
        mono && "font-mono",
        className
      )}
    >
      {children}
    </span>
  )
}

/** A sentence carries the message; the colored icon supports it. */
export function HealthLine({
  tone,
  children,
}: {
  tone: "good" | "warning" | "critical" | "muted"
  children: React.ReactNode
}) {
  const Icon = tone === "good" ? CheckCircle2 : tone === "critical" ? XCircle : tone === "warning" ? AlertTriangle : CircleDashed
  const color = {
    good: "text-good",
    warning: "text-warning",
    critical: "text-critical",
    muted: "text-muted-foreground",
  }[tone]
  return (
    <p className="flex items-start gap-1.5">
      <Icon className={cn("mt-0.5 size-3.5 shrink-0", color)} strokeWidth={1.75} aria-hidden />
      <span>{children}</span>
    </p>
  )
}
