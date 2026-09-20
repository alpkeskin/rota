import * as React from "react"
import { count } from "@/lib/format"

/** Ranked magnitudes: one-color background bar, label on top, count + share on the right. */
export function BarList({
  items,
  max,
}: {
  items: { label: React.ReactNode; value: number; share?: number; key?: string }[]
  max?: number
}) {
  const top = max ?? Math.max(1, ...items.map((i) => i.value))
  const total = items.reduce((s, i) => s + i.value, 0) || 1
  return (
    <ul className="space-y-1">
      {items.map((it, i) => {
        const pct = Math.round((it.value / top) * 100)
        const share = it.share ?? Math.round((it.value / total) * 100)
        return (
          <li key={it.key ?? i} className="grid grid-cols-[1fr_auto] items-center gap-x-3">
            <div className="relative min-w-0">
              <div className="bg-chart-1/18 absolute inset-y-0 left-0 rounded-[3px]" style={{ width: `${pct}%` }} aria-hidden />
              <span className="relative block truncate py-1 pl-2">{it.label}</span>
            </div>
            <span className="num text-muted-foreground">
              <span className="text-foreground font-medium">{count(it.value)}</span>
              <span className="ml-2 inline-block w-9 text-right">{share}%</span>
            </span>
          </li>
        )
      })}
    </ul>
  )
}
