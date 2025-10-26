"use client"

import * as React from "react"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Status, StatusIndicator, StatusLabel } from "@/components/ui/shadcn-io/status"
import {
  Activity,
  Download,
  Trash2,
  Search,
  Play,
  Pause,
  CheckCircle2,
  XCircle,
  AlertCircle,
  Info,
} from "lucide-react"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

import { api } from "@/lib/api"
import { LogEntry as LogEntryType } from "@/lib/types"

type LogLevel = "info" | "warning" | "error" | "success"

interface LogEntry {
  id: string
  timestamp: Date
  level: LogLevel
  message: string
  details?: string
  metadata?: Record<string, any>
}

function LogLevelIcon({ level }: { level: LogLevel }) {
  switch (level) {
    case "info":
      return <Info className="h-4 w-4 text-blue-500" />
    case "warning":
      return <AlertCircle className="h-4 w-4 text-yellow-500" />
    case "error":
      return <XCircle className="h-4 w-4 text-red-500" />
    case "success":
      return <CheckCircle2 className="h-4 w-4 text-green-500" />
  }
}

function LogLevelBadge({ level }: { level: LogLevel }) {
  const colors = {
    info: "bg-blue-500/10 text-blue-500 hover:bg-blue-500/20",
    warning: "bg-yellow-500/10 text-yellow-500 hover:bg-yellow-500/20",
    error: "bg-red-500/10 text-red-500 hover:bg-red-500/20",
    success: "bg-green-500/10 text-green-500 hover:bg-green-500/20",
  }

  return (
    <Badge variant="outline" className={colors[level]}>
      {level.toUpperCase()}
    </Badge>
  )
}

export default function LogsPage() {
  const [logs, setLogs] = React.useState<LogEntry[]>([])
  const [isStreaming, setIsStreaming] = React.useState(false)
  const [autoScroll, setAutoScroll] = React.useState(true)
  const [searchQuery, setSearchQuery] = React.useState("")
  const [levelFilters, setLevelFilters] = React.useState({
    info: true,
    warning: true,
    error: true,
    success: true,
  })
  const scrollRef = React.useRef<HTMLDivElement>(null)
  const wsRef = React.useRef<WebSocket | null>(null)

  // WebSocket streaming
  React.useEffect(() => {
    if (!isStreaming) {
      if (wsRef.current) {
        wsRef.current.close()
        wsRef.current = null
      }
      return
    }

    const activeLevels = Object.entries(levelFilters)
      .filter(([_, enabled]) => enabled)
      .map(([level]) => level)

    try {
      wsRef.current = api.createLogsWebSocket(
        (log: LogEntryType) => {
          setLogs((prev) => {
            const newLogs = [
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
            // Keep last 10000 logs to prevent memory issues
            return newLogs.length > 10000 ? newLogs.slice(-10000) : newLogs
          })
        },
        activeLevels.length > 0 ? activeLevels : undefined,
        "proxy" // Only stream proxy logs
      )

      wsRef.current.onerror = (error) => {
        console.error("WebSocket error:", error)
        setIsStreaming(false)
      }

      wsRef.current.onclose = () => {
        console.log("WebSocket closed")
      }
    } catch (error) {
      console.error("Failed to connect to WebSocket:", error)
      setIsStreaming(false)
    }

    return () => {
      if (wsRef.current) {
        wsRef.current.close()
        wsRef.current = null
      }
    }
  }, [isStreaming, levelFilters])

  // Auto-scroll to bottom
  React.useEffect(() => {
    if (autoScroll && scrollRef.current) {
      scrollRef.current.scrollIntoView({ behavior: "smooth" })
    }
  }, [logs, autoScroll])

  const filteredLogs = logs.filter((log) => {
    const matchesSearch = log.message.toLowerCase().includes(searchQuery.toLowerCase())
    const matchesLevel = levelFilters[log.level]
    return matchesSearch && matchesLevel
  })

  const handleToggleLevel = (level: LogLevel) => {
    setLevelFilters((prev) => ({ ...prev, [level]: !prev[level] }))
  }

  const handleClearLogs = () => {
    setLogs([])
  }

  const handleExportLogs = async () => {
    try {
      const blob = await api.exportLogs("txt", { source: "proxy" })
      const url = URL.createObjectURL(blob)
      const a = document.createElement("a")
      a.href = url
      a.download = `rota-proxy-logs-${Date.now()}.txt`
      a.click()
      URL.revokeObjectURL(url)
    } catch (error) {
      console.error("Failed to export logs:", error)
      alert("Failed to export logs")
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Proxy Logs</h1>
          <p className="text-muted-foreground">
            Real-time streaming logs from proxy requests and operations
          </p>
          <p className="text-xs text-muted-foreground mt-1">
            Maximum 10,000 logs can be streamed. Older logs will be automatically removed.
          </p>
        </div>
        <Status status={isStreaming ? "online" : "maintenance"}>
          <StatusIndicator />
          <StatusLabel>{isStreaming ? "Streaming" : "Paused"}</StatusLabel>
        </Status>
      </div>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle>Log Stream</CardTitle>
              <CardDescription>
                {filteredLogs.length} log entries
              </CardDescription>
            </div>
            <div className="flex items-center gap-2">
              <Button
                variant={isStreaming ? "default" : "outline"}
                size="sm"
                onClick={() => setIsStreaming(!isStreaming)}
              >
                {isStreaming ? (
                  <>
                    <Pause className="mr-2 h-4 w-4" />
                    Pause
                  </>
                ) : (
                  <>
                    <Play className="mr-2 h-4 w-4" />
                    Start
                  </>
                )}
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={handleExportLogs}
                disabled={logs.length === 0}
              >
                <Download className="mr-2 h-4 w-4" />
                Export
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={handleClearLogs}
                disabled={logs.length === 0}
              >
                <Trash2 className="mr-2 h-4 w-4" />
                Clear
              </Button>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <div className="space-y-4">
            {/* Filters */}
            <div className="flex items-center gap-4">
              <div className="relative flex-1">
                <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  placeholder="Search logs..."
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  className="pl-9"
                />
              </div>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="outline" size="sm">
                    Filters
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-48">
                  <DropdownMenuLabel>Log Levels</DropdownMenuLabel>
                  <DropdownMenuSeparator />
                  <DropdownMenuCheckboxItem
                    checked={levelFilters.info}
                    onCheckedChange={() => handleToggleLevel("info")}
                  >
                    <Info className="mr-2 h-4 w-4 text-blue-500" />
                    Info
                  </DropdownMenuCheckboxItem>
                  <DropdownMenuCheckboxItem
                    checked={levelFilters.warning}
                    onCheckedChange={() => handleToggleLevel("warning")}
                  >
                    <AlertCircle className="mr-2 h-4 w-4 text-yellow-500" />
                    Warning
                  </DropdownMenuCheckboxItem>
                  <DropdownMenuCheckboxItem
                    checked={levelFilters.error}
                    onCheckedChange={() => handleToggleLevel("error")}
                  >
                    <XCircle className="mr-2 h-4 w-4 text-red-500" />
                    Error
                  </DropdownMenuCheckboxItem>
                  <DropdownMenuCheckboxItem
                    checked={levelFilters.success}
                    onCheckedChange={() => handleToggleLevel("success")}
                  >
                    <CheckCircle2 className="mr-2 h-4 w-4 text-green-500" />
                    Success
                  </DropdownMenuCheckboxItem>
                </DropdownMenuContent>
              </DropdownMenu>
              <div className="flex items-center space-x-2">
                <Switch
                  id="auto-scroll"
                  checked={autoScroll}
                  onCheckedChange={setAutoScroll}
                />
                <Label htmlFor="auto-scroll" className="text-sm">
                  Auto-scroll
                </Label>
              </div>
            </div>

            {/* Log viewer */}
            <div className="rounded-lg border bg-card">
              <ScrollArea className="h-[600px]">
                <div className="space-y-0.5 p-4 font-mono text-sm">
                  {filteredLogs.length === 0 ? (
                    <div className="flex h-[568px] items-center justify-center text-muted-foreground">
                      {logs.length === 0 ? (
                        <div className="text-center">
                          <Activity className="mx-auto mb-2 h-8 w-8 opacity-50" />
                          <p>No logs yet. Start streaming to see logs.</p>
                        </div>
                      ) : (
                        <p>No logs match your filters.</p>
                      )}
                    </div>
                  ) : (
                    filteredLogs.map((log) => (
                      <div
                        key={log.id}
                        className="flex items-start gap-3 rounded-md px-3 py-2 hover:bg-accent/50 transition-colors"
                      >
                        <LogLevelIcon level={log.level} />
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="text-xs text-muted-foreground" suppressHydrationWarning>
                              {log.timestamp.toLocaleTimeString('en-US')}
                            </span>
                            <LogLevelBadge level={log.level} />
                          </div>
                          <p className="mt-1">{log.message}</p>
                          {log.details && (
                            <p className="mt-1 text-xs text-muted-foreground">
                              {log.details}
                            </p>
                          )}
                          {log.metadata && Object.keys(log.metadata).length > 0 && (
                            <div className="mt-1 flex flex-wrap gap-2">
                              {Object.entries(log.metadata).map(([key, value]) => (
                                <span key={key} className="inline-flex items-center gap-1 text-xs text-muted-foreground bg-muted/50 px-2 py-0.5 rounded">
                                  <span className="font-medium">{key}:</span>
                                  <span>{typeof value === 'object' ? JSON.stringify(value) : String(value)}</span>
                                </span>
                              ))}
                            </div>
                          )}
                        </div>
                      </div>
                    ))
                  )}
                  <div ref={scrollRef} />
                </div>
              </ScrollArea>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
