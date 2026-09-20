"use client"

import * as React from "react"
import { Suspense } from "react"
import Link from "next/link"
import { Area, AreaChart, Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts"
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart"
import { PageHeader, Section, Footnote, LoadingLine } from "@/components/page-header"
import { StatStrip, type Stat } from "@/components/stat-strip"
import { Segment } from "@/components/controls"
import { StatusLabel } from "@/components/status"
import { SplitBar } from "@/components/usage-bar"
import { useUrlState } from "@/hooks/use-url-state"
import { api } from "@/lib/api"
import { DashboardStats, ChartDataPoint } from "@/lib/types"
import { count, ms, percent, signedPercent } from "@/lib/format"
import { Radio, RadioTower } from "lucide-react"

// Chart intervals the core accepts. The default (4h buckets over 24h) is
// parameterless in the URL.
const RANGES = [
  { value: "1h", label: "1h · 24h", hint: "Hourly buckets over the last 24 hours." },
  { value: "4h", label: "4h · 24h", hint: "4-hour buckets over the last 24 hours." },
  { value: "1d", label: "1d · 7d", hint: "Daily buckets over the last 7 days." },
]

const responseConfig = {
  value: { label: "Avg response", color: "var(--chart-1)" },
} satisfies ChartConfig

const outcomeConfig = {
  success: { label: "Succeeded", color: "var(--good)" },
  failure: { label: "Failed", color: "var(--critical)" },
} satisfies ChartConfig

const URL_DEFAULTS = { range: "4h" }

function OverviewPage() {
  const [url] = useUrlState(URL_DEFAULTS)
  const range = RANGES.some((r) => r.value === url.range) ? url.range : "4h"

  const [stats, setStats] = React.useState<DashboardStats | null>(null)
  const [responseTime, setResponseTime] = React.useState<ChartDataPoint[]>([])
  const [outcomes, setOutcomes] = React.useState<ChartDataPoint[]>([])
  const [live, setLive] = React.useState(false)
  const [loading, setLoading] = React.useState(true)

  React.useEffect(() => {
    let cancelled = false
    api
      .getDashboardStats()
      .then((s) => !cancelled && setStats(s))
      .catch(() => {})
      .finally(() => !cancelled && setLoading(false))

    const ws = api.createDashboardWebSocket(
      (data) => setStats(data),
      (connected) => setLive(connected)
    )
    return () => {
      cancelled = true
      ws.close()
    }
  }, [])

  React.useEffect(() => {
    let cancelled = false
    Promise.all([api.getResponseTimeChart(range), api.getSuccessRateChart(range)])
      .then(([r, s]) => {
        if (cancelled) return
        setResponseTime(r.data)
        setOutcomes(s.data)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [range])

  const rate = stats?.avg_success_rate ?? 0
  // Thresholds only mean something once there is traffic to measure.
  const rateTone: Stat["tone"] = !stats || stats.total_requests === 0 ? "default" : rate >= 90 ? "good" : rate >= 70 ? "warning" : "critical"
  const operational = stats && stats.total_proxies > 0 ? (stats.active_proxies / stats.total_proxies) * 100 : 0

  const headline: Stat[] = [
    {
      label: "Active proxies",
      value: stats ? count(stats.active_proxies) : "—",
      hint: stats ? `of ${count(stats.total_proxies)} in inventory · ${Math.round(operational)}% usable` : undefined,
    },
    {
      label: "Requests, all time",
      value: stats ? count(stats.total_requests) : "—",
      // request_growth compares the last 24h with the 24h before it.
      hint: stats ? `last 24h ${signedPercent(stats.request_growth)} vs the 24h before` : undefined,
    },
    {
      label: "Success rate",
      value: stats ? percent(stats.avg_success_rate) : "—",
      hint: stats ? `${signedPercent(stats.success_rate_growth)} vs yesterday` : undefined,
      tone: rateTone,
    },
    {
      label: "Avg response",
      value: stats ? ms(stats.avg_response_time) : "—",
      hint: stats
        ? `${stats.response_time_delta > 0 ? "+" : stats.response_time_delta < 0 ? "−" : ""}${count(Math.abs(stats.response_time_delta))} ms vs yesterday`
        : undefined,
    },
  ]

  const peakResponse = Math.max(0, ...responseTime.map((d) => d.value ?? 0))
  // Success/failure arrive as per-bucket percentages, so summarise by
  // averaging buckets and naming the worst one.
  const worstBucket = outcomes.reduce<ChartDataPoint | null>(
    (worst, d) => (worst === null || (d.failure ?? 0) > (worst.failure ?? 0) ? d : worst),
    null
  )
  const avgSuccess = outcomes.length ? outcomes.reduce((s, d) => s + (d.success ?? 0), 0) / outcomes.length : null
  const rangeHint = RANGES.find((r) => r.value === range)?.hint

  if (loading && !stats) return <LoadingLine />

  return (
    <>
      <PageHeader title="Overview" description="What the proxy fleet is doing right now. Headline numbers update live; charts follow the selected range.">
        {live ? (
          <StatusLabel icon={RadioTower} tone="good">Live</StatusLabel>
        ) : (
          <StatusLabel icon={Radio} tone="muted">Polling</StatusLabel>
        )}
        <Segment
          label="Chart range"
          items={RANGES.map((r) => ({
            label: r.label,
            active: range === r.value,
            href: r.value === "4h" ? "/dashboard" : `/dashboard?range=${r.value}`,
          }))}
        />
      </PageHeader>

      <StatStrip stats={headline} columns={4} />

      {stats && (
        <Section
          title="Fleet"
          description="Share of the inventory in each state. Failed proxies are retried on the next health check; idle ones have not been checked yet."
        >
          <SplitBar
            segments={[
              { value: stats.active_proxies, tone: "good", label: "Active" },
              { value: Math.max(0, stats.total_proxies - stats.active_proxies), tone: "muted", label: "Failed or idle" },
            ]}
          />
        </Section>
      )}

      <Section
        title="Response time"
        description={`${rangeHint} Averages successful requests only.`}
        actions={
          <Link href="/dashboard/proxies?sort=avg_response_time&order=desc" className="text-muted-foreground hover:text-foreground font-medium">
            Slowest proxies →
          </Link>
        }
      >
        <div className="mb-2 flex items-baseline gap-2">
          <h3 className="label">Average, ms</h3>
          <span className="num text-muted-foreground ml-auto">
            <span className="text-foreground font-semibold">{count(peakResponse)}</span>
            <span className="ml-1.5">peak</span>
          </span>
        </div>
        {responseTime.length === 0 ? (
          <p className="text-muted-foreground py-8 text-center">No requests in this range — nothing to average yet.</p>
        ) : (
          <ChartContainer config={responseConfig} className="aspect-auto h-[11rem] w-full">
            <AreaChart data={responseTime} margin={{ left: 0, right: 8, top: 8, bottom: 0 }}>
              <defs>
                <linearGradient id="fill-response" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="var(--chart-1)" stopOpacity={0.22} />
                  <stop offset="100%" stopColor="var(--chart-1)" stopOpacity={0.02} />
                </linearGradient>
              </defs>
              <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="2 4" />
              <XAxis dataKey="time" tickLine={false} axisLine={false} tickMargin={8} minTickGap={28} tick={{ fill: "var(--muted-foreground)", fontSize: 11 }} />
              <YAxis width={36} tickLine={false} axisLine={false} allowDecimals={false} domain={[0, Math.max(peakResponse, 1)]} tick={{ fill: "var(--muted-foreground)", fontSize: 11 }} />
              <ChartTooltip cursor={{ stroke: "var(--border)" }} content={<ChartTooltipContent indicator="line" />} />
              <Area
                type="monotone"
                dataKey="value"
                stroke="var(--chart-1)"
                strokeWidth={2}
                fill="url(#fill-response)"
                dot={false}
                activeDot={{ r: 3.5, strokeWidth: 2, stroke: "var(--background)" }}
              />
            </AreaChart>
          </ChartContainer>
        )}
      </Section>

      <Section
        title="Outcomes"
        description={`${rangeHint} Each bar is one bucket split into succeeded and failed share of its requests — shares, not counts.`}
        actions={
          <Link href="/dashboard/logs" className="text-muted-foreground hover:text-foreground font-medium">
            Open logs →
          </Link>
        }
        className="border-b-0"
      >
        {outcomes.length === 0 ? (
          <p className="text-muted-foreground py-8 text-center">No requests in this range.</p>
        ) : (
          <ChartContainer config={outcomeConfig} className="aspect-auto h-[11rem] w-full">
            <BarChart data={outcomes} margin={{ left: 0, right: 8, top: 8, bottom: 0 }} barCategoryGap="20%">
              <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="2 4" />
              <XAxis dataKey="time" tickLine={false} axisLine={false} tickMargin={8} minTickGap={28} tick={{ fill: "var(--muted-foreground)", fontSize: 11 }} />
              <YAxis width={36} tickLine={false} axisLine={false} allowDecimals={false} domain={[0, 100]} tickFormatter={(v) => `${v}%`} tick={{ fill: "var(--muted-foreground)", fontSize: 11 }} />
              <ChartTooltip cursor={{ fill: "var(--accent)" }} content={<ChartTooltipContent formatter={(v, name) => (
                <span className="flex w-full items-center gap-2">
                  <span className="text-muted-foreground">{outcomeConfig[name as keyof typeof outcomeConfig]?.label ?? name}</span>
                  <span className="num ml-auto font-medium">{v}%</span>
                </span>
              )} />} />
              <Bar dataKey="success" stackId="a" fill="var(--good)" radius={[0, 0, 0, 0]} />
              <Bar dataKey="failure" stackId="a" fill="var(--critical)" radius={[2, 2, 0, 0]} />
            </BarChart>
          </ChartContainer>
        )}
        <Footnote
          items={[
            { label: "Mean success across buckets", value: avgSuccess === null ? "—" : percent(avgSuccess, 0) },
            { label: "Worst bucket", value: worstBucket ? `${worstBucket.time} · ${worstBucket.failure ?? 0}% failed` : "—" },
            { label: "Buckets", value: count(outcomes.length) },
          ]}
        />
      </Section>
    </>
  )
}

export default function Page() {
  return (
    <Suspense fallback={<LoadingLine />}>
      <OverviewPage />
    </Suspense>
  )
}
