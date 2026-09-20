import * as React from "react"
import { cn } from "@/lib/utils"

export interface DLItem {
  label: React.ReactNode
  value: React.ReactNode
  mono?: boolean
  /** Span the whole row. */
  wide?: boolean
}

/** Detail fields in a responsive grid. */
export function DefinitionList({ items, columns = 4, className }: { items: DLItem[]; columns?: 2 | 3 | 4; className?: string }) {
  const cols = { 2: "sm:grid-cols-2", 3: "sm:grid-cols-2 lg:grid-cols-3", 4: "sm:grid-cols-2 lg:grid-cols-4" }[columns]
  return (
    <dl className={cn("grid gap-x-8 gap-y-3", cols, className)}>
      {items.map((it, i) => (
        <div key={i} className={cn("min-w-0", it.wide && "sm:col-span-full")}>
          <dt className="label">{it.label}</dt>
          <dd className={cn("mt-0.5", it.mono && "font-mono", !it.wide && "truncate")}>{it.value ?? "—"}</dd>
        </div>
      ))}
    </dl>
  )
}
