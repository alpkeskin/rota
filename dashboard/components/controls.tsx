"use client"

import * as React from "react"
import Link from "next/link"
import { ChevronDown, RefreshCw, Search } from "lucide-react"
import { cn } from "@/lib/utils"

const control =
  "border-border bg-background h-8 rounded-md border font-medium transition-colors focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none disabled:opacity-50"

/** Search box with the icon inside; commits on Enter or after a pause. */
export function SearchInput({
  value,
  onChange,
  placeholder = "Search…",
  className,
  delay = 400,
  ...rest
}: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  className?: string
  delay?: number
} & Omit<React.ComponentProps<"input">, "value" | "onChange">) {
  const [draft, setDraft] = React.useState(value)
  // Only adopt an external `value` (back button, "Clear") — never one we sent
  // ourselves, otherwise a slow URL round-trip would overwrite what the user
  // typed in the meantime.
  const sent = React.useRef(value)
  React.useEffect(() => {
    if (value !== sent.current) {
      sent.current = value
      setDraft(value)
    }
  }, [value])
  React.useEffect(() => {
    if (draft === sent.current) return
    const t = setTimeout(() => {
      sent.current = draft
      onChange(draft)
    }, delay)
    return () => clearTimeout(t)
  }, [draft, onChange, delay])
  return (
    <div className={cn("relative", className)}>
      <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" aria-hidden />
      <input
        type="search"
        value={draft}
        placeholder={placeholder}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            sent.current = draft
            onChange(draft)
          }
        }}
        className={cn(control, "placeholder:text-muted-foreground w-56 pr-2.5 pl-8 font-normal")}
        {...rest}
      />
    </div>
  )
}

/** Native select styled as a control; changing it applies immediately. */
export function NativeSelect({
  value,
  onChange,
  options,
  className,
  "aria-label": ariaLabel,
  disabled,
}: {
  value: string
  onChange: (v: string) => void
  options: { value: string; label: string; disabled?: boolean }[]
  className?: string
  "aria-label"?: string
  disabled?: boolean
}) {
  return (
    <div className={cn("relative inline-flex", className)}>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-label={ariaLabel}
        disabled={disabled}
        className={cn(control, "appearance-none pr-7 pl-2.5")}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value} disabled={o.disabled}>
            {o.label}
          </option>
        ))}
      </select>
      <ChevronDown className="text-muted-foreground pointer-events-none absolute top-1/2 right-2 size-3.5 -translate-y-1/2" aria-hidden />
    </div>
  )
}

/** Segmented control made of links (or buttons when no href). */
export function Segment({
  items,
  label,
}: {
  items: { label: React.ReactNode; active: boolean; href?: string; onClick?: () => void }[]
  label: string
}) {
  return (
    <div className="border-border inline-flex overflow-hidden rounded-md border" role="group" aria-label={label}>
      {items.map((it, i) => {
        const cls = cn(
          "px-2.5 py-1 font-medium transition-colors focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none focus-visible:ring-inset",
          i > 0 && "border-border border-l",
          it.active ? "bg-accent text-foreground" : "text-muted-foreground hover:text-foreground hover:bg-accent/50"
        )
        return it.href ? (
          <Link key={i} href={it.href} scroll={false} aria-current={it.active ? "true" : undefined} className={cls}>
            {it.label}
          </Link>
        ) : (
          <button key={i} type="button" onClick={it.onClick} aria-pressed={it.active} className={cls}>
            {it.label}
          </button>
        )
      })}
    </div>
  )
}

const LIVE_KEY = "rota:live-refresh"
const LIVE_OPTIONS = [
  { value: "0", label: "Live: off" },
  { value: "5", label: "Live: 5s" },
  { value: "10", label: "Live: 10s" },
  { value: "30", label: "Live: 30s" },
  { value: "60", label: "Live: 1m" },
]

function readLive(fallback: string): string {
  try {
    return localStorage.getItem(LIVE_KEY) ?? fallback
  } catch {
    return fallback
  }
}
const liveListeners = new Set<() => void>()
function subscribeLive(cb: () => void) {
  liveListeners.add(cb)
  return () => {
    liveListeners.delete(cb)
  }
}

/** Auto-refresh interval, remembered per browser. Calls `onTick` on schedule. */
export function LiveRefresh({ onTick, defaultSeconds }: { onTick: () => void | Promise<void>; defaultSeconds?: number }) {
  const fallback = String(defaultSeconds ?? 0)
  const value = React.useSyncExternalStore(subscribeLive, () => readLive(fallback), () => fallback)
  const [spinning, setSpinning] = React.useState(false)
  const tick = React.useCallback(async () => {
    setSpinning(true)
    try {
      await onTick()
    } finally {
      setTimeout(() => setSpinning(false), 600)
    }
  }, [onTick])

  React.useEffect(() => {
    const secs = Number(value)
    if (!secs) return
    const id = setInterval(tick, secs * 1000)
    return () => clearInterval(id)
  }, [value, tick])

  return (
    <div className="relative inline-flex">
      <RefreshCw
        className={cn("text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2", spinning && "animate-spin")}
        aria-hidden
      />
      <select
        value={value}
        aria-label="Auto refresh"
        onChange={(e) => {
          try {
            localStorage.setItem(LIVE_KEY, e.target.value)
          } catch {}
          liveListeners.forEach((cb) => cb())
        }}
        className={cn(control, "appearance-none pr-7 pl-8")}
      >
        {LIVE_OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      <ChevronDown className="text-muted-foreground pointer-events-none absolute top-1/2 right-2 size-3.5 -translate-y-1/2" aria-hidden />
    </div>
  )
}

/** A control-height link/button that reads as a secondary action. */
export const controlClass = control
