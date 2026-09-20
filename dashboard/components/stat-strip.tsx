import * as React from "react"
import { cn } from "@/lib/utils"

export type StatTone = "default" | "good" | "warning" | "critical"

export interface Stat {
  label: React.ReactNode
  value: React.ReactNode
  /** What the number is measured against — never a repeat of the label. */
  hint?: React.ReactNode
  /** Only when a real threshold exists. */
  tone?: StatTone
}

const tones: Record<StatTone, string> = {
  default: "",
  good: "text-good",
  warning: "text-warning",
  critical: "text-critical",
}

/** Headline numbers as one hairline-divided strip, all on the same baseline. */
export function StatStrip({ stats, columns = 6 }: { stats: Stat[]; columns?: 3 | 4 | 5 | 6 }) {
  const cols = {
    3: "sm:grid-cols-3 xl:grid-cols-3",
    4: "sm:grid-cols-2 xl:grid-cols-4",
    5: "sm:grid-cols-3 xl:grid-cols-5",
    6: "sm:grid-cols-3 xl:grid-cols-6",
  }[columns]
  return (
    // 1px gaps over a border-colored backdrop draw the hairlines between cells
    // for any column count; cells paint their own background over it.
    <dl className={cn("border-border bg-border grid grid-cols-2 gap-px border-b", cols)}>
      {stats.map((s, i) => (
        <div key={i} className="bg-background px-4 py-3.5 md:px-6">
          <dt className="label">{s.label}</dt>
          <dd className={cn("num mt-1 text-[1.375rem] leading-none font-semibold", tones[s.tone ?? "default"])}>
            {s.value ?? "—"}
          </dd>
          {s.hint && <p className="text-muted-foreground mt-1.5 text-[0.6875rem] leading-4">{s.hint}</p>}
        </div>
      ))}
    </dl>
  )
}
