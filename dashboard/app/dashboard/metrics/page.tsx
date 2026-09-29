"use client"

import * as React from "react"
import { Globe } from "lucide-react"
import { PageHeader, Section, LoadingLine } from "@/components/page-header"
import { StatStrip, type StatTone } from "@/components/stat-strip"
import { DefinitionList } from "@/components/definition-list"
import { UsageBar } from "@/components/usage-bar"
import { LiveRefresh } from "@/components/controls"
import { ErrorStatus, HealthLine, OkStatus, Tag } from "@/components/status"
import { api } from "@/lib/api"
import { SystemMetrics } from "@/lib/types"
import { bytes, count, percent } from "@/lib/format"

// Resource pressure thresholds: only these earn a status color.
function usageTone(pct: number): StatTone {
  if (pct >= 90) return "critical"
  if (pct >= 75) return "warning"
  return "default"
}

export default function MetricsPage() {
  const [metrics, setMetrics] = React.useState<SystemMetrics | null>(null)
  const [updatedAt, setUpdatedAt] = React.useState<Date | null>(null)

  const fetchMetrics = React.useCallback(async () => {
    try {
      const data = await api.getSystemMetrics()
      setMetrics(data)
      setUpdatedAt(new Date())
    } catch (error) {
      console.error("Failed to fetch system metrics:", error)
    }
  }, [])

  React.useEffect(() => {
    fetchMetrics()
  }, [fetchMetrics])

  if (!metrics) return <LoadingLine />

  const { memory, cpu, disk, runtime, geo } = metrics
  const worst = Math.max(memory.percentage, cpu.percentage, disk.percentage)
  const hasPipelines = Boolean(geo)
  const pressure =
    worst >= 90
      ? { tone: "critical" as const, text: "A resource is above 90% — the core may start refusing work." }
      : worst >= 75
        ? { tone: "warning" as const, text: "A resource is above 75%; fine for now, worth watching." }
        : { tone: "good" as const, text: "Every resource is below 75%." }

  return (
    <>
      <PageHeader
        title="System"
        description="Host resources and Go runtime counters for the core process. Percentages are of the host, not of the container limit."
      >
        {updatedAt && (
          <span className="text-muted-foreground num" suppressHydrationWarning>
            as of {updatedAt.toLocaleTimeString("en-GB")}
          </span>
        )}
        <LiveRefresh onTick={fetchMetrics} defaultSeconds={5} />
      </PageHeader>

      <StatStrip
        columns={4}
        stats={[
          { label: "Memory", value: percent(memory.percentage), hint: `${bytes(memory.used)} of ${bytes(memory.total)}`, tone: usageTone(memory.percentage) },
          { label: "CPU", value: percent(cpu.percentage), hint: `${cpu.cores} cores`, tone: usageTone(cpu.percentage) },
          { label: "Disk", value: percent(disk.percentage), hint: `${bytes(disk.free)} free`, tone: usageTone(disk.percentage) },
          { label: "Goroutines", value: count(runtime.goroutines), hint: `on ${count(runtime.threads)} OS threads` },
        ]}
      />

      <Section title="Pressure" description="Each bar is the share of the host resource in use.">
        <div className="grid gap-6 lg:grid-cols-3">
          {[
            { label: "Memory", pct: memory.percentage, detail: `${bytes(memory.used)} used · ${bytes(memory.available)} available` },
            { label: "CPU", pct: cpu.percentage, detail: `${cpu.cores} cores` },
            { label: "Disk", pct: disk.percentage, detail: `${bytes(disk.used)} used · ${bytes(disk.free)} free` },
          ].map((r) => (
            <div key={r.label}>
              <div className="mb-2 flex items-baseline gap-2">
                <h3 className="font-medium">{r.label}</h3>
                <span className="num text-muted-foreground ml-auto">
                  <span className="text-foreground font-semibold">{percent(r.pct)}</span>
                </span>
              </div>
              <UsageBar value={r.pct} tone={usageTone(r.pct) === "default" ? "default" : usageTone(r.pct)} />
              <p className="text-muted-foreground mt-1.5 text-[0.6875rem] leading-4">{r.detail}</p>
            </div>
          ))}
        </div>
        <div className="mt-6">
          <HealthLine tone={pressure.tone}>{pressure.text}</HealthLine>
        </div>
      </Section>

      <Section title="Go runtime" description="Heap and scheduler counters from the process itself.">
        <DefinitionList
          columns={4}
          items={[
            { label: "Heap allocated", value: bytes(runtime.mem_alloc) },
            { label: "Reserved from OS", value: bytes(runtime.mem_sys) },
            { label: "Heap in use", value: runtime.mem_sys > 0 ? percent((runtime.mem_alloc / runtime.mem_sys) * 100) : "—" },
            { label: "GC pauses", value: count(runtime.gc_pause_count) },
            { label: "Goroutines", value: count(runtime.goroutines) },
            { label: "OS threads", value: count(runtime.threads) },
            { label: "Goroutines per thread", value: runtime.threads > 0 ? (runtime.goroutines / runtime.threads).toFixed(1) : "—" },
          ]}
        />
      </Section>

      {hasPipelines && (
        <Section title="Background pipelines" description="Background jobs: GeoIP enrichment." className="border-b-0">
          <div className="grid gap-6 md:grid-cols-2 lg:grid-cols-3">
            {geo && <GeoPanel geo={geo} />}
          </div>
        </Section>
      )}
    </>
  )
}

// ── Background pipeline panels ─────────────────────────────────────────────

type GeoSection = NonNullable<SystemMetrics["geo"]>

const GEO_QUEUE_CAPACITY = 1000

function queueTone(pct: number): "default" | "warning" | "critical" {
  if (pct >= 70) return "critical"
  if (pct >= 30) return "warning"
  return "default"
}

const panelHeader = (Icon: typeof Globe, title: React.ReactNode, status: React.ReactNode) => (
  <div className="flex items-center gap-2">
    <Icon className="text-muted-foreground size-4" aria-hidden />
    <h3 className="font-medium">{title}</h3>
    <span className="ml-auto">{status}</span>
  </div>
)

function GeoPanel({ geo }: { geo: GeoSection }) {
  const queuePct = Math.min(100, (geo.queue_pending / GEO_QUEUE_CAPACITY) * 100)
  const atLimit = geo.usage_percent_1m >= 70
  return (
    <div className="border-border rounded-md p-4">
      {panelHeader(
        Globe,
        <span className="flex items-center gap-1.5">
          GeoIP Enrichment
          {geo.provider && <Tag mono>{geo.provider}</Tag>}
        </span>,
        atLimit ? <ErrorStatus>at limit</ErrorStatus> : <OkStatus>ok</OkStatus>
      )}
      <div className="mt-3 space-y-3">
        <div>
          <div className="mb-1 flex items-baseline justify-between text-xs">
            <span className="text-muted-foreground">Enrichment queue</span>
            <span className="num">{count(geo.queue_pending)} pending · {count(geo.queued_in_memory)} in memory</span>
          </div>
          <UsageBar value={queuePct} tone={queueTone(queuePct)} />
        </div>
        <div>
          <div className="mb-1 flex items-baseline justify-between text-xs">
            <span className="text-muted-foreground">Batch requests (1m)</span>
            <span className="num">{geo.batch_requests_limit > 0 ? `${count(geo.batch_requests_last_minute)} / ${count(geo.batch_requests_limit)}` : "—"}</span>
          </div>
          <UsageBar value={geo.usage_percent_1m} tone={queueTone(geo.usage_percent_1m)} />
          <div className="mt-1 flex items-baseline justify-between text-xs">
            <span className="text-muted-foreground">Rate limit usage</span>
            <span className="num font-medium">{percent(geo.usage_percent_1m)}</span>
          </div>
        </div>
        <div className="flex items-baseline justify-between text-xs">
          <span className="text-muted-foreground">IPs updated (10m)</span>
          <span className="num font-medium">{count(geo.ips_updated_last_10m)}</span>
        </div>
      </div>
    </div>
  )
}
