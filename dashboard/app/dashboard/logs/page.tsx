"use client"

import * as React from "react"
import { AlertTriangle, CheckCircle2, Info, XCircle, type LucideIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PageHeader, EmptyLine } from "@/components/page-header"
import { SearchInput } from "@/components/controls"
import { StatusLabel } from "@/components/status"
import { api } from "@/lib/api"
import { LogEntry as LogEntryType } from "@/lib/types"
import { formatTime } from "@/lib/format"
import { toast } from "@/lib/toast"
import { cn } from "@/lib/utils"

type LogLevel = "info" | "warning" | "error" | "success"

interface LogEntry {
  id: string
  timestamp: Date
  level: LogLevel
  message: string
  details?: string
  metadata?: Record<string, unknown>
}

const LEVELS: { level: LogLevel; label: string; icon: LucideIcon; tone: "good" | "warning" | "critical" | "muted" }[] = [
  { level: "success", label: "Success", icon: CheckCircle2, tone: "good" },
  { level: "info", label: "Info", icon: Info, tone: "muted" },
  { level: "warning", label: "Warning", icon: AlertTriangle, tone: "warning" },
  { level: "error", label: "Error", icon: XCircle, tone: "critical" },
]

const MAX_LOGS = 10000

export default function LogsPage() {
  const [logs, setLogs] = React.useState<LogEntry[]>([])
  const [isStreaming, setIsStreaming] = React.useState(false)
  const [autoScroll, setAutoScroll] = React.useState(true)
  const [searchQuery, setSearchQuery] = React.useState("")
  const [levelFilters, setLevelFilters] = React.useState<Record<LogLevel, boolean>>({
    info: true,
    warning: true,
    error: true,
    success: true,
  })
  const listRef = React.useRef<HTMLDivElement>(null)
  const wsRef = React.useRef<WebSocket | null>(null)

  React.useEffect(() => {
    if (!isStreaming) {
      wsRef.current?.close()
      wsRef.current = null
      return
    }

    const activeLevels = (Object.keys(levelFilters) as LogLevel[]).filter((l) => levelFilters[l])

    try {
      wsRef.current = api.createLogsWebSocket(
        (log: LogEntryType) => {
          setLogs((prev) => {
            const next = [
              ...prev,
              {
                id: log.id,
                timestamp: new Date(log.timestamp),
                level: log.level,
                message: log.message,
                details: log.details,
                metadata: log.metadata,
              },
            ]
            return next.length > MAX_LOGS ? next.slice(-MAX_LOGS) : next
          })
        },
        activeLevels.length > 0 ? activeLevels : undefined,
        "proxy"
      )
      wsRef.current.onerror = () => setIsStreaming(false)
    } catch {
      setIsStreaming(false)
    }

    return () => {
      wsRef.current?.close()
      wsRef.current = null
    }
  }, [isStreaming, levelFilters])

  React.useEffect(() => {
    const el = listRef.current
    if (autoScroll && el) el.scrollTop = el.scrollHeight
  }, [logs, autoScroll])

  const filtered = logs.filter((log) => {
    const q = searchQuery.toLowerCase()
    return (!q || log.message.toLowerCase().includes(q)) && levelFilters[log.level]
  })

  const handleExport = async () => {
    try {
      const blob = await api.exportLogs("txt", { source: "proxy" })
      const href = URL.createObjectURL(blob)
      const a = document.createElement("a")
      a.href = href
      a.download = `rota-proxy-logs-${Date.now()}.txt`
      a.click()
      URL.revokeObjectURL(href)
    } catch {
      toast.error("Failed to export logs")
    }
  }

  const counts = logs.reduce<Record<LogLevel, number>>(
    (acc, l) => {
      acc[l.level]++
      return acc
    },
    { info: 0, warning: 0, error: 0, success: 0 }
  )

  return (
    // The page owns the viewport height on desktop so the list scrolls inside
    // itself; "Follow" then never scrolls the header and controls away.
    <div className="flex flex-col md:h-svh">
      <PageHeader
        title="Logs"
        description={`Proxy request events, streamed as they happen. The buffer keeps the last ${MAX_LOGS.toLocaleString("en-US")} lines; export pulls the stored history.`}
      >
        {isStreaming ? <StatusLabel icon={CheckCircle2} tone="good">Streaming</StatusLabel> : <StatusLabel icon={Info} tone="muted">Paused</StatusLabel>}
        <Button variant="outline" onClick={handleExport}>
          Export
        </Button>
        <Button variant="outline" onClick={() => setLogs([])} disabled={logs.length === 0}>
          Clear
        </Button>
        <Button onClick={() => setIsStreaming((v) => !v)}>{isStreaming ? "Pause" : "Start"}</Button>
      </PageHeader>

      <div className="border-border flex flex-wrap items-center gap-x-4 gap-y-2 border-b px-4 py-3 md:px-6">
        <SearchInput value={searchQuery} onChange={setSearchQuery} placeholder="Filter messages" delay={150} aria-label="Filter log messages" />
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1" role="group" aria-label="Levels">
          {LEVELS.map(({ level, label, icon, tone }) => (
            <label key={level} className="inline-flex cursor-pointer items-center gap-1.5">
              <input
                type="checkbox"
                className="accent-primary size-3.5"
                checked={levelFilters[level]}
                onChange={() => setLevelFilters((prev) => ({ ...prev, [level]: !prev[level] }))}
              />
              <StatusLabel icon={icon} tone={tone} className={cn(!levelFilters[level] && "opacity-50")}>
                {label}
              </StatusLabel>
              <span className="num text-muted-foreground">{counts[level]}</span>
            </label>
          ))}
        </div>
        <label className="ml-auto inline-flex cursor-pointer items-center gap-1.5">
          <input type="checkbox" className="accent-primary size-3.5" checked={autoScroll} onChange={(e) => setAutoScroll(e.target.checked)} />
          Follow
        </label>
      </div>

      <div ref={listRef} className="min-h-[16rem] flex-1 overflow-y-auto font-mono text-[0.75rem] leading-5 md:min-h-0">
        {filtered.length === 0 ? (
          <EmptyLine className="font-sans">
            {logs.length === 0
              ? isStreaming
                ? "Waiting for the first event…"
                : "Nothing captured yet — press Start to stream."
              : "No line matches these filters."}
          </EmptyLine>
        ) : (
          <ol>
            {filtered.map((log) => {
              const meta = LEVELS.find((l) => l.level === log.level) ?? LEVELS[1]
              return (
                <li key={log.id} className="border-border hover:bg-muted/40 grid grid-cols-[auto_auto_1fr] items-start gap-x-3 border-b px-4 py-1.5 md:px-6">
                  <span className="text-muted-foreground num" suppressHydrationWarning>
                    {formatTime(log.timestamp)}
                  </span>
                  <StatusLabel icon={meta.icon} tone={meta.tone} className="w-[5.5rem] font-sans">
                    {meta.label}
                  </StatusLabel>
                  <div className="min-w-0">
                    <p className="break-words">{log.message}</p>
                    {log.details && <p className="text-muted-foreground break-words">{log.details}</p>}
                    {log.metadata && Object.keys(log.metadata).length > 0 && (
                      <p className="text-muted-foreground break-words">
                        {Object.entries(log.metadata).map(([k, v]) => (
                          <span key={k} className="mr-3">
                            {k}={typeof v === "object" ? JSON.stringify(v) : String(v)}
                          </span>
                        ))}
                      </p>
                    )}
                  </div>
                </li>
              )
            })}
          </ol>
        )}
      </div>
    </div>
  )
}
