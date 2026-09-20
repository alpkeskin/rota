import * as React from "react"
import { cn } from "@/lib/utils"
import { count } from "@/lib/format"

/** A single proportion bar. Status colors only when a real threshold is crossed. */
export function UsageBar({
  value,
  tone = "default",
  className,
}: {
  value: number
  tone?: "default" | "good" | "warning" | "critical"
  className?: string
}) {
  const fill = {
    default: "bg-muted-foreground/60",
    good: "bg-good",
    warning: "bg-warning",
    critical: "bg-critical",
  }[tone]
  const pct = Math.max(0, Math.min(100, value))
  return (
    <div className={cn("bg-muted h-1.5 w-full overflow-hidden rounded-[2px]", className)} role="presentation">
      <div className={cn("h-full rounded-[2px]", fill)} style={{ width: `${pct}%` }} />
    </div>
  )
}

/** Segmented proportion bar (e.g. active / failed / idle). */
export function SplitBar({
  segments,
}: {
  segments: { value: number; tone: "good" | "warning" | "critical" | "muted"; label: string }[]
}) {
  const total = segments.reduce((s, x) => s + x.value, 0)
  const fill = { good: "bg-good", warning: "bg-warning", critical: "bg-critical", muted: "bg-muted-foreground/35" }
  return (
    <div>
      <div className="flex h-2.5 w-full gap-0.5" role="img" aria-label={segments.map((s) => `${s.label} ${s.value}`).join(", ")}>
        {total === 0 ? (
          <div className="bg-muted h-full w-full rounded-[2px]" />
        ) : (
          segments
            .filter((s) => s.value > 0)
            .map((s, i) => (
              <div
                key={i}
                className={cn("h-full rounded-[2px]", fill[s.tone])}
                style={{ width: `${(s.value / total) * 100}%`, minWidth: "0.375rem" }}
              />
            ))
        )}
      </div>
      <dl className="text-muted-foreground mt-2 flex flex-wrap gap-x-5 gap-y-1">
        {segments.map((s, i) => (
          <div key={i} className="flex items-center gap-1.5">
            <span className={cn("size-2 rounded-[2px]", fill[s.tone])} aria-hidden />
            <dt>{s.label}</dt>
            <dd className="num text-foreground font-medium">{count(s.value)}</dd>
            {total > 0 && <dd className="num">{Math.round((s.value / total) * 100)}%</dd>}
          </div>
        ))}
      </dl>
    </div>
  )
}
