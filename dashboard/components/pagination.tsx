"use client"

import * as React from "react"
import { ChevronLeft, ChevronRight } from "lucide-react"
import { count } from "@/lib/format"
import { cn } from "@/lib/utils"

export function Pagination({
  page,
  limit,
  total,
  onPage,
}: {
  page: number
  limit: number
  total: number
  onPage: (page: number) => void
}) {
  const totalPages = Math.max(1, Math.ceil(total / limit))
  const from = total === 0 ? 0 : (page - 1) * limit + 1
  const to = Math.min(page * limit, total)
  const step =
    "border-border inline-flex items-center gap-1 rounded-md border px-2 py-1 font-medium transition-colors hover:bg-accent focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none disabled:text-muted-foreground/50 disabled:border-border/60 disabled:pointer-events-none"
  return (
    <div className="text-muted-foreground flex items-center justify-between gap-4 pt-3">
      <p className="num">
        {from}–{to} of {count(total)}
      </p>
      <div className="flex items-center gap-1.5">
        <button type="button" className={cn(step)} disabled={page <= 1} onClick={() => onPage(page - 1)}>
          <ChevronLeft className="size-3.5" aria-hidden /> Previous
        </button>
        <button type="button" className={cn(step)} disabled={page >= totalPages} onClick={() => onPage(page + 1)}>
          Next <ChevronRight className="size-3.5" aria-hidden />
        </button>
      </div>
    </div>
  )
}
