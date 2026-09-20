"use client"

import * as React from "react"
import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react"
import { TableHead } from "@/components/ui/table"
import { cn } from "@/lib/utils"

/** Sortable column header. First click sorts descending ("worst first"). */
export function SortHeader({
  field,
  sort,
  order,
  onSort,
  children,
  className,
  align = "left",
}: {
  field: string
  sort: string
  order: "asc" | "desc"
  onSort: (field: string, order: "asc" | "desc") => void
  children: React.ReactNode
  className?: string
  align?: "left" | "right"
}) {
  const active = sort === field
  const Icon = active ? (order === "asc" ? ArrowUp : ArrowDown) : ChevronsUpDown
  const next: "asc" | "desc" = active ? (order === "desc" ? "asc" : "desc") : "desc"
  return (
    <TableHead
      aria-sort={active ? (order === "asc" ? "ascending" : "descending") : "none"}
      className={cn(align === "right" && "text-right", className)}
    >
      <button
        type="button"
        onClick={() => onSort(field, next)}
        className={cn(
          "inline-flex items-center gap-1 rounded-sm font-medium transition-colors hover:text-foreground focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
          align === "right" && "flex-row-reverse",
          !active && "text-foreground"
        )}
      >
        {children}
        <Icon className={cn("size-3", !active && "opacity-40")} aria-hidden />
      </button>
    </TableHead>
  )
}
