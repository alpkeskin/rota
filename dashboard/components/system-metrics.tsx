"use client"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Progress } from "@/components/ui/progress"
import {
  Cpu,
  HardDrive,
  MemoryStick,
  Zap,
  Activity,
  Server,
} from "lucide-react"
import { formatBytes, formatNumber, getUsageColor } from "@/lib/format-utils"
import { cn } from "@/lib/utils"

interface SystemMetricsProps {
  data?: {
    memory: {
      total: number
      used: number
      available: number
      percentage: number
    }
    cpu: {
      percentage: number
      cores: number
    }
    disk: {
      total: number
      used: number
      free: number
      percentage: number
    }
    runtime: {
      goroutines: number
      threads: number
      gc_pause_count: number
      mem_alloc: number
      mem_sys: number
    }
  }
}

export function SystemMetrics({ data }: SystemMetricsProps) {
  // Mock data - will be replaced with real API data
  const metrics = data || {
    memory: {
      total: 17179869184,
      used: 10737418240,
      available: 6442450944,
      percentage: 62.5,
    },
    cpu: {
      percentage: 35.2,
      cores: 8,
    },
    disk: {
      total: 500107862016,
      used: 320171548672,
      free: 179936313344,
      percentage: 64.0,
    },
    runtime: {
      goroutines: 42,
      threads: 8,
      gc_pause_count: 156,
      mem_alloc: 5242880,
      mem_sys: 75497472,
    },
  }

  const getProgressVariant = (percentage: number) => {
    if (percentage >= 90) return "destructive"
    if (percentage >= 75) return "warning"
    return "success"
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold tracking-tight">System Metrics</h2>
          <p className="text-sm text-muted-foreground">
            Real-time system resource monitoring
          </p>
        </div>
      </div>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        {/* Memory Card */}
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Memory</CardTitle>
            <MemoryStick className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="space-y-1">
              <div className="flex items-baseline justify-between">
                <span
                  className={cn(
                    "text-2xl font-bold",
                    getUsageColor(metrics.memory.percentage)
                  )}
                >
                  {metrics.memory.percentage.toFixed(1)}%
                </span>
                <span className="text-xs text-muted-foreground">
                  {formatBytes(metrics.memory.used)} / {formatBytes(metrics.memory.total)}
                </span>
              </div>
              <Progress
                value={metrics.memory.percentage}
                variant={getProgressVariant(metrics.memory.percentage)}
                className="h-2"
              />
            </div>
            <div className="grid grid-cols-2 gap-2 text-xs">
              <div className="space-y-1">
                <p className="text-muted-foreground">Used</p>
                <p className="font-medium">{formatBytes(metrics.memory.used)}</p>
              </div>
              <div className="space-y-1">
                <p className="text-muted-foreground">Available</p>
                <p className="font-medium">{formatBytes(metrics.memory.available)}</p>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* CPU Card */}
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">CPU</CardTitle>
            <Cpu className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="space-y-1">
              <div className="flex items-baseline justify-between">
                <span
                  className={cn(
                    "text-2xl font-bold",
                    getUsageColor(metrics.cpu.percentage)
                  )}
                >
                  {metrics.cpu.percentage.toFixed(1)}%
                </span>
                <span className="text-xs text-muted-foreground">
                  {metrics.cpu.cores} cores
                </span>
              </div>
              <Progress
                value={metrics.cpu.percentage}
                variant={getProgressVariant(metrics.cpu.percentage)}
                className="h-2"
              />
            </div>
            <div className="grid grid-cols-2 gap-2 text-xs">
              <div className="space-y-1">
                <p className="text-muted-foreground">Cores</p>
                <p className="font-medium">{metrics.cpu.cores}</p>
              </div>
              <div className="space-y-1">
                <p className="text-muted-foreground">Load</p>
                <p className="font-medium">{metrics.cpu.percentage.toFixed(1)}%</p>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* Disk Card */}
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Disk</CardTitle>
            <HardDrive className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="space-y-1">
              <div className="flex items-baseline justify-between">
                <span
                  className={cn(
                    "text-2xl font-bold",
                    getUsageColor(metrics.disk.percentage)
                  )}
                >
                  {metrics.disk.percentage.toFixed(1)}%
                </span>
                <span className="text-xs text-muted-foreground">
                  {formatBytes(metrics.disk.used)} / {formatBytes(metrics.disk.total)}
                </span>
              </div>
              <Progress
                value={metrics.disk.percentage}
                variant={getProgressVariant(metrics.disk.percentage)}
                className="h-2"
              />
            </div>
            <div className="grid grid-cols-2 gap-2 text-xs">
              <div className="space-y-1">
                <p className="text-muted-foreground">Used</p>
                <p className="font-medium">{formatBytes(metrics.disk.used)}</p>
              </div>
              <div className="space-y-1">
                <p className="text-muted-foreground">Free</p>
                <p className="font-medium">{formatBytes(metrics.disk.free)}</p>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* Runtime Card */}
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Runtime</CardTitle>
            <Zap className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="space-y-1">
              <div className="flex items-baseline justify-between">
                <span className="text-2xl font-bold text-primary">
                  {formatNumber(metrics.runtime.goroutines)}
                </span>
                <span className="text-xs text-muted-foreground">goroutines</span>
              </div>
            </div>
            <div className="grid grid-cols-2 gap-2 text-xs">
              <div className="space-y-1">
                <p className="text-muted-foreground">Threads</p>
                <p className="font-medium">{metrics.runtime.threads}</p>
              </div>
              <div className="space-y-1">
                <p className="text-muted-foreground">GC Pauses</p>
                <p className="font-medium">{formatNumber(metrics.runtime.gc_pause_count)}</p>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Detailed Runtime Metrics */}
      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-base flex items-center gap-2">
              <Activity className="h-4 w-4" />
              Go Runtime Memory
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-sm text-muted-foreground">Memory Allocated</span>
                <span className="text-sm font-medium">{formatBytes(metrics.runtime.mem_alloc)}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-sm text-muted-foreground">System Memory</span>
                <span className="text-sm font-medium">{formatBytes(metrics.runtime.mem_sys)}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-sm text-muted-foreground">Memory Efficiency</span>
                <span className="text-sm font-medium">
                  {((metrics.runtime.mem_alloc / metrics.runtime.mem_sys) * 100).toFixed(1)}%
                </span>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base flex items-center gap-2">
              <Server className="h-4 w-4" />
              Concurrency Stats
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-sm text-muted-foreground">Active Goroutines</span>
                <span className="text-sm font-medium">{formatNumber(metrics.runtime.goroutines)}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-sm text-muted-foreground">OS Threads</span>
                <span className="text-sm font-medium">{metrics.runtime.threads}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-sm text-muted-foreground">Goroutines/Thread</span>
                <span className="text-sm font-medium">
                  {(metrics.runtime.goroutines / metrics.runtime.threads).toFixed(1)}
                </span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
