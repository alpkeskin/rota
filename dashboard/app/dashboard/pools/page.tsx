"use client"

import { useEffect, useState, useCallback, useRef, Suspense } from "react"
import { ChevronDown, X } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import {
  ProxyPool,
  PoolProxy,
  GeoSummaryItem,
  HCJob,
  CreatePoolRequest,
  PoolAlertRule,
  CreatePoolAlertRuleRequest,
  Proxy,
} from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogFooter } from "@/components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Switch } from "@/components/ui/switch"
import { Label } from "@/components/ui/label"
import { Checkbox } from "@/components/ui/checkbox"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { GeoSelector } from "@/components/geo-selector"
import { TagInput } from "@/components/tag-input"
import { PageHeader, TabStrip, EmptyLine, LoadingLine } from "@/components/page-header"
import { StatStrip } from "@/components/stat-strip"
import { DefinitionList } from "@/components/definition-list"
import { SplitBar, UsageBar } from "@/components/usage-bar"
import { ErrorStatus, OkStatus, OnOff, ProxyStatus, RunningStatus, Tag } from "@/components/status"
import { useUrlState } from "@/hooks/use-url-state"
import { count, formatDateTime, ms, percent, relative, seconds } from "@/lib/format"
import { cn } from "@/lib/utils"

const ROTATION_LABELS: Record<string, string> = {
  roundrobin: "Round robin",
  random: "Random",
  stick: "Sticky",
}

const FLAG = (cc: string) => `https://flagcdn.com/16x12/${cc.toLowerCase()}.png`

const DEFAULT_POOL_FORM: CreatePoolRequest = {
  name: "",
  description: "",
  country_code: undefined,
  region_name: undefined,
  city_name: undefined,
  rotation_method: "roundrobin",
  stick_count: 10,
  health_check_url: "https://api.ipify.org",
  health_check_cron: "*/30 * * * *",
  health_check_enabled: true,
  auto_sync: true,
  sync_mode: "auto",
  enabled: true,
  isp_filters: [],
  tag_filters: [],
}

const URL_DEFAULTS = { tab: "pools", pool: "" }

function PoolsPage() {
  const [url, setUrl] = useUrlState(URL_DEFAULTS)
  const tab = url.tab === "geo" ? "geo" : "pools"
  const selectedId = parseInt(url.pool) || null

  const [pools, setPools] = useState<ProxyPool[]>([])
  const [geoCountries, setGeoCountries] = useState<GeoSummaryItem[]>([])
  const [loading, setLoading] = useState(true)

  // Detail
  const [poolProxies, setPoolProxies] = useState<PoolProxy[]>([])
  const [poolProxiesLoading, setPoolProxiesLoading] = useState(false)
  const [hcJob, setHcJob] = useState<HCJob | null>(null)
  const [hcRunning, setHcRunning] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const hcPollRef = useRef<ReturnType<typeof setInterval> | null>(null)
  // Tracks the most recently requested pool so a slower response for an older
  // selection can't overwrite a newer one.
  const selectedPoolReqRef = useRef<number | null>(null)

  // Dialogs
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editPool, setEditPool] = useState<ProxyPool | null>(null)
  const [form, setForm] = useState<CreatePoolRequest>(DEFAULT_POOL_FORM)
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ProxyPool | null>(null)
  const [newGeoCountry, setNewGeoCountry] = useState("")
  const [newGeoCity, setNewGeoCity] = useState("")
  const [tagList, setTagList] = useState<string[]>([])
  const [ispList, setIspList] = useState<string[]>([])

  // Member picker
  const [pickerOpen, setPickerOpen] = useState(false)
  const [pickerSearch, setPickerSearch] = useState("")
  const [pickerResults, setPickerResults] = useState<Proxy[]>([])
  const [pickerLoading, setPickerLoading] = useState(false)
  const [pickerSelected, setPickerSelected] = useState<number[]>([])
  const [addingProxies, setAddingProxies] = useState(false)

  // Alert rules
  const [alertRules, setAlertRules] = useState<PoolAlertRule[]>([])
  const [alertDialogOpen, setAlertDialogOpen] = useState(false)
  const [alertForm, setAlertForm] = useState<CreatePoolAlertRuleRequest>({
    enabled: true,
    min_active_proxies: 5,
    webhook_url: "",
    webhook_method: "POST",
    cooldown_minutes: 30,
  })
  const [editAlertRule, setEditAlertRule] = useState<PoolAlertRule | null>(null)
  const [savingAlert, setSavingAlert] = useState(false)
  const [deleteRuleId, setDeleteRuleId] = useState<number | null>(null)

  const selectedPool = pools.find((p) => p.id === selectedId) ?? null

  const loadAll = useCallback(async () => {
    try {
      const [poolsRes, countriesRes] = await Promise.all([api.getPools(), api.getGeoByCountry()])
      setPools(poolsRes.pools)
      setGeoCountries(countriesRes.geo)
    } catch {
      toast.error("Failed to load pools")
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadAll()
  }, [loadAll])

  const refreshFilterSuggestions = useCallback(() => {
    api.getTagList().then(setTagList).catch(() => {})
    api.getISPList().then(setIspList).catch(() => {})
  }, [])

  useEffect(() => {
    refreshFilterSuggestions()
  }, [refreshFilterSuggestions])

  const stopHcPoll = useCallback(() => {
    if (hcPollRef.current) {
      clearInterval(hcPollRef.current)
      hcPollRef.current = null
    }
  }, [])

  const loadDetail = useCallback(async (poolId: number) => {
    setPoolProxiesLoading(true)
    selectedPoolReqRef.current = poolId
    try {
      const [proxiesRes, rules] = await Promise.all([api.getPoolProxies(poolId), api.getAlertRules(poolId).catch(() => [])])
      if (selectedPoolReqRef.current !== poolId) return
      setPoolProxies(proxiesRes.proxies)
      setAlertRules(rules)
    } catch {
      if (selectedPoolReqRef.current !== poolId) return
      toast.error("Failed to load pool members")
    } finally {
      if (selectedPoolReqRef.current === poolId) setPoolProxiesLoading(false)
    }
  }, [])

  useEffect(() => {
    // A health-check poll belongs to the pool it was started on.
    stopHcPoll()
    setHcRunning(false)
    setHcJob(null)
    if (selectedId) loadDetail(selectedId)
    else {
      setPoolProxies([])
      setAlertRules([])
    }
  }, [selectedId, loadDetail, stopHcPoll])

  const selectPool = (id: number | null) => setUrl({ pool: id ? String(id) : "" }, { replace: true })

  // ── Pool CRUD ─────────────────────────────────────────────────────────────

  const openCreate = () => {
    setEditPool(null)
    setForm({ ...DEFAULT_POOL_FORM, geo_filters: [] })
    setNewGeoCountry("")
    setNewGeoCity("")
    refreshFilterSuggestions()
    setDialogOpen(true)
  }

  const openEdit = (p: ProxyPool) => {
    setEditPool(p)
    setForm({
      name: p.name,
      description: p.description,
      country_code: p.country_code,
      region_name: p.region_name,
      city_name: p.city_name,
      rotation_method: p.rotation_method,
      stick_count: p.stick_count,
      health_check_url: p.health_check_url,
      health_check_cron: p.health_check_cron,
      health_check_enabled: p.health_check_enabled,
      auto_sync: p.auto_sync,
      sync_mode: p.sync_mode ?? "auto",
      enabled: p.enabled,
      geo_filters: p.geo_filters ?? [],
      isp_filters: p.isp_filters ?? [],
      tag_filters: p.tag_filters ?? [],
    })
    setNewGeoCountry("")
    setNewGeoCity("")
    refreshFilterSuggestions()
    setDialogOpen(true)
  }

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim()) {
      toast.error("Name is required")
      return
    }
    setSaving(true)
    try {
      if (editPool) {
        await api.updatePool(editPool.id, form)
        toast.success("Pool updated")
      } else {
        const created = await api.createPool(form)
        toast.success("Pool created")
        selectPool(created.id)
      }
      setDialogOpen(false)
      loadAll()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to save pool")
    } finally {
      setSaving(false)
    }
  }

  const confirmDelete = async () => {
    if (!deleteTarget) return
    try {
      await api.deletePool(deleteTarget.id)
      toast.success("Pool deleted")
      if (selectedId === deleteTarget.id) selectPool(null)
      loadAll()
    } catch {
      toast.error("Failed to delete pool")
    } finally {
      setDeleteTarget(null)
    }
  }

  const addGeoFilter = () => {
    const cc = newGeoCountry.trim().toUpperCase()
    if (!cc) {
      toast.error("Enter a country code")
      return
    }
    const existing = form.geo_filters ?? []
    if (existing.some((f) => f.country_code === cc && (f.city_name ?? "") === newGeoCity.trim())) {
      toast.error("Already added")
      return
    }
    setForm({
      ...form,
      geo_filters: [...existing, { country_code: cc, ...(newGeoCity.trim() ? { city_name: newGeoCity.trim() } : {}) }],
    })
    setNewGeoCountry("")
    setNewGeoCity("")
  }

  // ── Detail actions ────────────────────────────────────────────────────────

  const handleExport = async (format: "txt" | "csv") => {
    if (!selectedPool) return
    try {
      const blob = await api.exportPool(selectedPool.id, format)
      const href = URL.createObjectURL(blob)
      const a = document.createElement("a")
      a.href = href
      a.download = `pool-${selectedPool.id}.${format}`
      a.click()
      URL.revokeObjectURL(href)
    } catch {
      toast.error("Failed to export pool")
    }
  }

  const handleSync = async () => {
    if (!selectedPool) return
    setSyncing(true)
    try {
      const res = await api.syncPool(selectedPool.id)
      toast.success(`Synced ${res.synced} proxies into the pool`)
      loadDetail(selectedPool.id)
      loadAll()
    } catch {
      toast.error("Sync failed")
    } finally {
      setSyncing(false)
    }
  }

  const handleHealthCheck = async () => {
    if (!selectedPool) return
    setHcRunning(true)
    setHcJob(null)
    stopHcPoll()
    const poolId = selectedPool.id
    try {
      const res = await api.healthCheckPool(poolId, selectedPool.health_check_url, 20)
      hcPollRef.current = setInterval(async () => {
        try {
          const job = await api.getHealthCheckJob(poolId, res.job_id)
          setHcJob(job)
          if (job.status === "done" || job.status === "failed") {
            stopHcPoll()
            setHcRunning(false)
            if (job.status === "done") toast.success(`Health check done: ${job.active} of ${job.progress} active`)
            else toast.error(`Health check failed: ${job.error}`)
            loadDetail(poolId)
            loadAll()
          }
        } catch {
          stopHcPoll()
          setHcRunning(false)
        }
      }, 1500)
    } catch {
      toast.error("Failed to start health check")
      setHcRunning(false)
    }
  }

  useEffect(() => () => stopHcPoll(), [stopHcPoll])

  // ── Members ───────────────────────────────────────────────────────────────

  const openPicker = () => {
    setPickerSearch("")
    setPickerSelected([])
    setPickerOpen(true)
  }

  useEffect(() => {
    if (!pickerOpen) return
    let cancelled = false
    setPickerLoading(true)
    const timer = setTimeout(async () => {
      try {
        const res = await api.getProxies({ page: 1, limit: 50, search: pickerSearch.trim() || undefined })
        if (!cancelled) setPickerResults(res.proxies)
      } catch {
        if (!cancelled) toast.error("Failed to load proxies")
      } finally {
        if (!cancelled) setPickerLoading(false)
      }
    }, 300)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [pickerOpen, pickerSearch])

  const handleAddProxiesToPool = async () => {
    if (!selectedPool || pickerSelected.length === 0) return
    setAddingProxies(true)
    try {
      const res = await api.addPoolProxies(selectedPool.id, pickerSelected)
      toast.success(`Added ${res.added} proxies`)
      setPickerOpen(false)
      loadDetail(selectedPool.id)
      loadAll()
    } catch {
      toast.error("Failed to add proxies")
    } finally {
      setAddingProxies(false)
    }
  }

  const handleRemoveProxyFromPool = async (proxyId: number) => {
    if (!selectedPool) return
    try {
      await api.removePoolProxies(selectedPool.id, [proxyId])
      setPoolProxies((prev) => prev.filter((p) => p.proxy_id !== proxyId))
      loadAll()
    } catch {
      toast.error("Failed to remove proxy")
    }
  }

  // ── Alert rules ───────────────────────────────────────────────────────────

  const openCreateAlertRule = () => {
    setEditAlertRule(null)
    setAlertForm({ enabled: true, min_active_proxies: 5, webhook_url: "", webhook_method: "POST", cooldown_minutes: 30 })
    setAlertDialogOpen(true)
  }

  const openEditAlertRule = (rule: PoolAlertRule) => {
    setEditAlertRule(rule)
    setAlertForm({
      enabled: rule.enabled,
      min_active_proxies: rule.min_active_proxies,
      webhook_url: rule.webhook_url,
      webhook_method: rule.webhook_method,
      cooldown_minutes: rule.cooldown_minutes,
    })
    setAlertDialogOpen(true)
  }

  const handleSaveAlertRule = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!selectedPool) return
    setSavingAlert(true)
    try {
      if (editAlertRule) {
        await api.updateAlertRule(selectedPool.id, editAlertRule.id, alertForm)
        toast.success("Alert rule updated")
      } else {
        await api.createAlertRule(selectedPool.id, alertForm)
        toast.success("Alert rule created")
      }
      setAlertDialogOpen(false)
      setAlertRules(await api.getAlertRules(selectedPool.id))
    } catch {
      toast.error("Failed to save alert rule")
    } finally {
      setSavingAlert(false)
    }
  }

  const confirmDeleteRule = async () => {
    if (!selectedPool || deleteRuleId === null) return
    try {
      await api.deleteAlertRule(selectedPool.id, deleteRuleId)
      setAlertRules((prev) => prev.filter((r) => r.id !== deleteRuleId))
      toast.success("Alert rule deleted")
    } catch {
      toast.error("Failed to delete alert rule")
    } finally {
      setDeleteRuleId(null)
    }
  }

  // ── Render ────────────────────────────────────────────────────────────────

  const totalInPools = pools.reduce((s, p) => s + p.total_proxies, 0)
  const activeInPools = pools.reduce((s, p) => s + p.active_proxies, 0)
  const degraded = pools.filter((p) => p.enabled && p.total_proxies > 0 && p.active_proxies === 0).length

  const geoFiltersOf = (p: ProxyPool) =>
    (p.geo_filters ?? []).map((f) => `${f.country_code}${f.city_name ? ` / ${f.city_name}` : ""}`)

  const rowBase = "border-border w-full border-b px-4 py-3 text-left transition-colors hover:bg-muted/40 md:px-6"

  return (
    <>
      <PageHeader
        title="Pools"
        description="Named groups of proxies with their own filters and rotation. Users are routed through pools; a pool with no active proxy hands over to the next fallback."
      >
        <Button onClick={openCreate}>New pool</Button>
      </PageHeader>

      <TabStrip
        label="Pool views"
        tabs={[
          { href: "/dashboard/pools", label: "Pools", active: tab === "pools" },
          { href: "/dashboard/pools?tab=geo", label: "Geo distribution", active: tab === "geo" },
        ]}
      />

      {loading ? (
        <LoadingLine />
      ) : tab === "geo" ? (
        <GeoSelector countries={geoCountries} existingPools={pools} onCreated={loadAll} />
      ) : (
        <>
          <StatStrip
            columns={4}
            stats={[
              { label: "Pools", value: count(pools.length), hint: `${count(pools.filter((p) => p.enabled).length)} enabled` },
              { label: "Proxies in pools", value: count(totalInPools), hint: "a proxy can sit in several pools" },
              { label: "Active in pools", value: count(activeInPools), hint: totalInPools ? `${Math.round((activeInPools / totalInPools) * 100)}% of members` : undefined },
              {
                label: "Empty pools",
                value: count(degraded),
                hint: degraded ? "enabled, with members, none active" : "every enabled pool has an active proxy",
                tone: degraded > 0 ? "critical" : pools.length > 0 ? "good" : "default",
              },
            ]}
          />

          {pools.length === 0 ? (
            <EmptyLine>No pools yet. Create one from filters, or pick locations under Geo distribution.</EmptyLine>
          ) : (
            <div className="grid lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
              {/* List */}
              <div className="border-border lg:border-r">
                <div className="border-border text-muted-foreground flex items-center justify-between border-b px-4 py-2 md:px-6">
                  <span className="label">Pool</span>
                  <span className="label num">active / members</span>
                </div>
                <ol>
                  {pools.map((pool) => {
                    const active = pool.id === selectedId
                    const share = pool.total_proxies > 0 ? (pool.active_proxies / pool.total_proxies) * 100 : 0
                    return (
                      <li key={pool.id}>
                        <button
                          type="button"
                          onClick={() => selectPool(pool.id)}
                          aria-current={active ? "true" : undefined}
                          className={cn(rowBase, active && "bg-accent/70 hover:bg-accent/70")}
                        >
                          <span className="flex items-baseline justify-between gap-3">
                            <span className="flex min-w-0 items-center gap-2">
                              <span className="truncate font-medium">{pool.name}</span>
                              {!pool.enabled && <Tag>disabled</Tag>}
                            </span>
                            <span className="num shrink-0">
                              <span className="font-medium">{count(pool.active_proxies)}</span>
                              <span className="text-muted-foreground"> / {count(pool.total_proxies)}</span>
                            </span>
                          </span>
                          <span className="text-muted-foreground mt-0.5 flex items-center gap-2 truncate text-[0.6875rem] leading-4">
                            <span>{ROTATION_LABELS[pool.rotation_method] || pool.rotation_method}</span>
                            {geoFiltersOf(pool).length > 0 && <span className="truncate font-mono">{geoFiltersOf(pool).join(", ")}</span>}
                            {(pool.tag_filters?.length ?? 0) > 0 && <span className="truncate">tags: {pool.tag_filters!.join(", ")}</span>}
                          </span>
                          <UsageBar value={share} className="mt-2" tone={pool.total_proxies > 0 && pool.active_proxies === 0 ? "critical" : "default"} />
                        </button>
                      </li>
                    )
                  })}
                </ol>
              </div>

              {/* Detail */}
              <div className="min-w-0">
                {!selectedPool ? (
                  <EmptyLine>Select a pool to see its members, health and alerts.</EmptyLine>
                ) : (
                  <>
                    <div className="border-border flex flex-wrap items-end justify-between gap-x-6 gap-y-3 border-b px-4 py-4 md:px-6">
                      <div className="min-w-0">
                        <h2 className="text-[0.9375rem] leading-tight font-semibold tracking-tight">{selectedPool.name}</h2>
                        <p className="text-muted-foreground mt-1">{selectedPool.description || <span className="font-mono">pool #{selectedPool.id}</span>}</p>
                      </div>
                      <div className="flex flex-wrap items-center gap-2">
                        <Button variant="outline" size="sm" onClick={handleSync} disabled={syncing} title="Rebuild membership from the pool's filters">
                          {syncing ? "Syncing…" : "Sync"}
                        </Button>
                        <Button variant="outline" size="sm" onClick={handleHealthCheck} disabled={hcRunning}>
                          {hcRunning ? "Checking…" : "Check health"}
                        </Button>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button variant="outline" size="sm">
                              More <ChevronDown aria-hidden />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem onClick={() => openEdit(selectedPool)}>Edit</DropdownMenuItem>
                            <DropdownMenuItem onClick={() => handleExport("txt")}>Export as TXT</DropdownMenuItem>
                            <DropdownMenuItem onClick={() => handleExport("csv")}>Export as CSV</DropdownMenuItem>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem variant="destructive" onClick={() => setDeleteTarget(selectedPool)}>Delete</DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </div>
                    </div>

                    <div className="border-border border-b px-4 py-5 md:px-6">
                      <DefinitionList
                        columns={3}
                        items={[
                          {
                            label: "Rotation",
                            value: `${ROTATION_LABELS[selectedPool.rotation_method] || selectedPool.rotation_method}${selectedPool.rotation_method === "stick" ? ` · ${selectedPool.stick_count} requests` : ""}`,
                          },
                          { label: "Membership", value: selectedPool.sync_mode === "manual" ? "Manual — never rebuilt" : selectedPool.auto_sync ? "Auto — rebuilt on import" : "Auto — sync by hand" },
                          { label: "Enabled", value: <OnOff on={selectedPool.enabled} onLabel="Yes" offLabel="No" /> },
                          { label: "Health check", value: selectedPool.health_check_enabled ? selectedPool.health_check_cron : "off", mono: selectedPool.health_check_enabled },
                          { label: "Check URL", value: selectedPool.health_check_url, mono: true, wide: false },
                          { label: "Created", value: relative(selectedPool.created_at) },
                          {
                            label: "Filters",
                            wide: true,
                            value:
                              geoFiltersOf(selectedPool).length + (selectedPool.tag_filters?.length ?? 0) + (selectedPool.isp_filters?.length ?? 0) === 0 ? (
                                <span className="text-muted-foreground">none — membership is manual</span>
                              ) : (
                                <span className="flex flex-wrap gap-1">
                                  {geoFiltersOf(selectedPool).map((g) => (
                                    <Tag key={g} mono>{g}</Tag>
                                  ))}
                                  {(selectedPool.tag_filters ?? []).map((t) => (
                                    <Tag key={`t-${t}`} strong>tag: {t}</Tag>
                                  ))}
                                  {(selectedPool.isp_filters ?? []).map((i) => (
                                    <Tag key={`i-${i}`}>isp: {i}</Tag>
                                  ))}
                                </span>
                              ),
                          },
                        ]}
                      />
                      <div className="mt-5">
                        <SplitBar
                          segments={[
                            { value: selectedPool.active_proxies, tone: "good", label: "Active" },
                            { value: selectedPool.failed_proxies, tone: "critical", label: "Failed" },
                            { value: Math.max(0, selectedPool.total_proxies - selectedPool.active_proxies - selectedPool.failed_proxies), tone: "muted", label: "Unchecked" },
                          ]}
                        />
                      </div>
                      {hcJob && (
                        <div className="mt-4 space-y-2">
                          <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
                            {hcJob.status === "running" || hcJob.status === "pending" ? (
                              <RunningStatus>Checking {hcJob.progress}{hcJob.total > 0 && ` of ${hcJob.total}`}</RunningStatus>
                            ) : hcJob.status === "done" ? (
                              <OkStatus>
                                Checked {hcJob.progress} in {hcJob.finished_at ? seconds(new Date(hcJob.finished_at).getTime() - new Date(hcJob.started_at).getTime()) : "—"}
                              </OkStatus>
                            ) : (
                              <ErrorStatus>{hcJob.error || "Check failed"}</ErrorStatus>
                            )}
                            <span className="num text-muted-foreground">
                              <span className="text-foreground font-medium">{hcJob.active}</span> active · <span className="text-foreground font-medium">{hcJob.failed}</span> failed
                            </span>
                          </div>
                          {(hcJob.status === "running" || hcJob.status === "pending") && hcJob.total > 0 && <UsageBar value={(hcJob.progress / hcJob.total) * 100} />}
                        </div>
                      )}
                    </div>

                    {/* Members */}
                    <div className="border-border border-b px-4 py-5 md:px-6">
                      <div className="mb-3 flex items-baseline justify-between gap-4">
                        <h3 className="label">
                          Members <span className="num">({count(poolProxies.length)})</span>
                        </h3>
                        <Button variant="outline" size="sm" onClick={openPicker}>
                          Add proxies
                        </Button>
                      </div>
                      {poolProxiesLoading ? (
                        <p className="text-muted-foreground py-6 text-center">Loading members…</p>
                      ) : poolProxies.length === 0 ? (
                        <p className="text-muted-foreground py-6 text-center">No members. Sync to fill from filters, or add proxies by hand.</p>
                      ) : (
                        <div className="max-h-[24rem] overflow-y-auto">
                          <Table>
                            <TableHeader>
                              <TableRow>
                                <TableHead>Address</TableHead>
                                <TableHead>Location</TableHead>
                                <TableHead>Status</TableHead>
                                <TableHead className="text-right">Success</TableHead>
                                <TableHead className="text-right">Response</TableHead>
                                <TableHead className="w-8" />
                              </TableRow>
                            </TableHeader>
                            <TableBody>
                              {poolProxies.map((pp) => (
                                <TableRow key={pp.proxy_id}>
                                  <TableCell className="font-mono">
                                    {pp.address}
                                    <span className="text-muted-foreground ml-2 text-[0.6875rem]">{pp.protocol}</span>
                                  </TableCell>
                                  <TableCell className="text-muted-foreground">
                                    <span className="inline-flex items-center gap-1.5">
                                      {pp.country_code && (
                                        // eslint-disable-next-line @next/next/no-img-element
                                        <img src={FLAG(pp.country_code)} alt="" width={16} height={12} />
                                      )}
                                      {pp.city_name || pp.country_name || "—"}
                                    </span>
                                  </TableCell>
                                  <TableCell>
                                    <ProxyStatus status={pp.status} />
                                  </TableCell>
                                  <TableCell className="num text-right">{percent(pp.success_rate)}</TableCell>
                                  <TableCell className="num text-muted-foreground text-right">{pp.avg_response_time ? ms(pp.avg_response_time) : "—"}</TableCell>
                                  <TableCell className="text-right">
                                    <Button variant="ghost" size="icon-sm" aria-label={`Remove ${pp.address}`} onClick={() => handleRemoveProxyFromPool(pp.proxy_id)}>
                                      <X aria-hidden />
                                    </Button>
                                  </TableCell>
                                </TableRow>
                              ))}
                            </TableBody>
                          </Table>
                        </div>
                      )}
                    </div>

                    {/* Alerts */}
                    <div className="px-4 py-5 md:px-6">
                      <div className="mb-3 flex items-baseline justify-between gap-4">
                        <h3 className="label">
                          Alert rules <span className="num">({count(alertRules.length)})</span>
                        </h3>
                        <Button variant="outline" size="sm" onClick={openCreateAlertRule}>
                          Add rule
                        </Button>
                      </div>
                      {alertRules.length === 0 ? (
                        <p className="text-muted-foreground py-4 text-center">No rules — nobody is told when this pool runs dry.</p>
                      ) : (
                        <ul className="divide-border divide-y">
                          {alertRules.map((rule) => (
                            <li key={rule.id} className="flex items-center gap-3 py-2">
                              <OnOff on={rule.enabled} onLabel="On" offLabel="Off" />
                              <span className="text-muted-foreground min-w-0 flex-1 truncate font-mono" title={rule.webhook_url}>
                                {rule.webhook_url}
                              </span>
                              <span className="num text-muted-foreground shrink-0">
                                below {rule.min_active_proxies} active · every {rule.cooldown_minutes}m
                              </span>
                              {rule.last_fired_at && (
                                <span className="text-muted-foreground shrink-0" title={formatDateTime(rule.last_fired_at)}>
                                  fired {relative(rule.last_fired_at)}
                                </span>
                              )}
                              <DropdownMenu>
                                <DropdownMenuTrigger asChild>
                                  <Button variant="ghost" size="icon-sm" aria-label="Rule actions">
                                    <ChevronDown aria-hidden />
                                  </Button>
                                </DropdownMenuTrigger>
                                <DropdownMenuContent align="end">
                                  <DropdownMenuItem onClick={() => openEditAlertRule(rule)}>Edit</DropdownMenuItem>
                                  <DropdownMenuItem variant="destructive" onClick={() => setDeleteRuleId(rule.id)}>Delete</DropdownMenuItem>
                                </DropdownMenuContent>
                              </DropdownMenu>
                            </li>
                          ))}
                        </ul>
                      )}
                    </div>
                  </>
                )}
              </div>
            </div>
          )}
        </>
      )}

      {/* Create / edit pool */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-[32rem]">
          <form onSubmit={handleSave} className="space-y-5">
            <DialogHeader>
              <DialogTitle>{editPool ? "Edit pool" : "Create pool"}</DialogTitle>
              <DialogDescription>A pool needs at least one country, tag or ISP filter to sync members on its own; otherwise add proxies by hand.</DialogDescription>
            </DialogHeader>

            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="pool-name">Name</Label>
                <Input id="pool-name" placeholder="US East" required value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="pool-desc">Description</Label>
                <Input id="pool-desc" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
              </div>
            </div>

            {/* Geo filters */}
            <div className="space-y-2">
              <Label>Countries</Label>
              {(form.geo_filters ?? []).length > 0 && (
                <div className="flex flex-wrap gap-1">
                  {(form.geo_filters ?? []).map((f, idx) => (
                    <span key={`${f.country_code}-${f.city_name ?? ""}-${idx}`} className="border-border inline-flex items-center gap-1 rounded border py-0.5 pr-0.5 pl-1.5 text-[0.6875rem] leading-4 font-medium">
                      {/* eslint-disable-next-line @next/next/no-img-element */}
                      <img src={FLAG(f.country_code)} alt="" width={16} height={12} />
                      <span className="font-mono">{f.country_code.toUpperCase()}</span>
                      {f.city_name && <span className="text-muted-foreground">/ {f.city_name}</span>}
                      <button
                        type="button"
                        className="text-muted-foreground hover:text-foreground rounded-sm p-0.5"
                        onClick={() => setForm({ ...form, geo_filters: (form.geo_filters ?? []).filter((_, i) => i !== idx) })}
                        aria-label="Remove"
                      >
                        <X className="size-3" aria-hidden />
                      </button>
                    </span>
                  ))}
                </div>
              )}
              <div className="grid grid-cols-[5rem_1fr_auto] items-end gap-2">
                <div className="space-y-1.5">
                  <Label htmlFor="new-geo-cc">Country</Label>
                  <Input
                    id="new-geo-cc"
                    className="font-mono"
                    placeholder="BR"
                    maxLength={3}
                    value={newGeoCountry}
                    onChange={(e) => setNewGeoCountry(e.target.value.toUpperCase())}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        e.preventDefault()
                        addGeoFilter()
                      }
                    }}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="new-geo-city">City (optional)</Label>
                  <Input
                    id="new-geo-city"
                    placeholder="whole country"
                    value={newGeoCity}
                    onChange={(e) => setNewGeoCity(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        e.preventDefault()
                        addGeoFilter()
                      }
                    }}
                  />
                </div>
                <Button type="button" variant="outline" onClick={addGeoFilter}>
                  Add
                </Button>
              </div>
              {geoCountries.length > 0 && (
                <div className="flex flex-wrap gap-1 pt-1">
                  {geoCountries.slice(0, 12).map((gc) => {
                    const already = (form.geo_filters ?? []).some((f) => f.country_code === gc.country_code && !f.city_name)
                    return (
                      <button
                        key={`${gc.country_code}-${gc.country_name}`}
                        type="button"
                        disabled={already}
                        className="border-border text-muted-foreground hover:bg-accent hover:text-foreground inline-flex items-center gap-1 rounded border px-1.5 py-0.5 font-mono text-[0.6875rem] leading-4 font-medium transition-colors disabled:opacity-40"
                        onClick={() => setForm({ ...form, geo_filters: [...(form.geo_filters ?? []), { country_code: gc.country_code }] })}
                        title={`${gc.country_name ?? gc.country_code} — ${gc.total} proxies`}
                      >
                        {gc.country_code}
                        <span className="num">{gc.total}</span>
                      </button>
                    )
                  })}
                </div>
              )}
            </div>

            <div className="space-y-1.5">
              <Label>Tag filters</Label>
              <TagInput value={form.tag_filters ?? []} onChange={(tag_filters) => setForm({ ...form, tag_filters })} suggestions={tagList} placeholder="local-socks" />
              <p className="text-muted-foreground text-[0.6875rem] leading-4">Matches proxies carrying all of these tags — the way to pool proxies that have no GeoIP data.</p>
            </div>

            <div className="space-y-1.5">
              <Label>ISP filters</Label>
              <TagInput value={form.isp_filters ?? []} onChange={(isp_filters) => setForm({ ...form, isp_filters })} suggestions={ispList} placeholder="Comcast" />
              <p className="text-muted-foreground text-[0.6875rem] leading-4">Matches proxies whose ISP contains any of these.</p>
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="pool-rotation">Rotation</Label>
                <Select value={form.rotation_method} onValueChange={(v) => setForm({ ...form, rotation_method: v as CreatePoolRequest["rotation_method"] })}>
                  <SelectTrigger id="pool-rotation" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="roundrobin">Round robin</SelectItem>
                    <SelectItem value="random">Random</SelectItem>
                    <SelectItem value="stick">Sticky, N requests</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              {form.rotation_method === "stick" && (
                <div className="space-y-1.5">
                  <Label htmlFor="pool-stick">Requests per proxy</Label>
                  <Input id="pool-stick" type="number" min={1} value={form.stick_count} onChange={(e) => setForm({ ...form, stick_count: parseInt(e.target.value) || 10 })} />
                </div>
              )}
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="pool-hc-url">Health check URL</Label>
                <Input id="pool-hc-url" className="font-mono" value={form.health_check_url} onChange={(e) => setForm({ ...form, health_check_url: e.target.value })} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="pool-hc-cron">Health check cron</Label>
                <Input id="pool-hc-cron" className="font-mono" placeholder="*/30 * * * *" value={form.health_check_cron} onChange={(e) => setForm({ ...form, health_check_cron: e.target.value })} />
              </div>
            </div>

            <div className="border-border divide-border divide-y border-y">
              {[
                { id: "hc-enabled", label: "Scheduled health check", hint: "Run the check on the cron above.", checked: form.health_check_enabled, set: (v: boolean) => setForm({ ...form, health_check_enabled: v }) },
                { id: "auto-sync", label: "Re-sync on import", hint: "Rebuild membership from filters whenever new proxies arrive.", checked: form.auto_sync, set: (v: boolean) => setForm({ ...form, auto_sync: v }) },
                { id: "sync-manual", label: "Manual membership", hint: "Never rebuild from filters; keeps proxies you added by hand.", checked: form.sync_mode === "manual", set: (v: boolean) => setForm({ ...form, sync_mode: v ? "manual" : "auto" }) },
                { id: "pool-enabled", label: "Enabled", checked: form.enabled, set: (v: boolean) => setForm({ ...form, enabled: v }) },
              ].map((r) => (
                <div key={r.id} className="flex items-start justify-between gap-6 py-2.5">
                  <div>
                    <Label htmlFor={r.id} className="text-foreground cursor-pointer">{r.label}</Label>
                    {r.hint && <p className="text-muted-foreground mt-0.5 text-[0.6875rem] leading-4">{r.hint}</p>}
                  </div>
                  <Switch id={r.id} checked={r.checked} onCheckedChange={r.set} />
                </div>
              ))}
            </div>

            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setDialogOpen(false)}>Cancel</Button>
              <Button type="submit" disabled={saving}>{saving ? "Saving…" : editPool ? "Save changes" : "Create pool"}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Add members */}
      <Dialog open={pickerOpen} onOpenChange={setPickerOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add proxies to {selectedPool?.name}</DialogTitle>
            <DialogDescription>
              {selectedPool?.sync_mode !== "manual"
                ? "This pool rebuilds from its filters on sync, which drops hand-added proxies. Switch it to manual membership (or add a matching tag) to keep them."
                : "Pick from the inventory. Already-added proxies are greyed out."}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <Input placeholder="Search by address" value={pickerSearch} onChange={(e) => setPickerSearch(e.target.value)} className="font-mono" />
            <div className="border-border max-h-64 overflow-y-auto rounded-md border">
              {pickerLoading ? (
                <p className="text-muted-foreground py-6 text-center">Loading…</p>
              ) : pickerResults.length === 0 ? (
                <p className="text-muted-foreground py-6 text-center">No proxy matches.</p>
              ) : (
                <div className="divide-border divide-y">
                  {pickerResults.map((p) => {
                    const inPool = poolProxies.some((pp) => pp.proxy_id === p.id)
                    const checked = pickerSelected.includes(p.id)
                    return (
                      <label key={p.id} className={cn("flex items-center gap-2 px-2.5 py-1.5", inPool ? "opacity-50" : "hover:bg-accent/50 cursor-pointer")}>
                        <Checkbox
                          checked={inPool || checked}
                          disabled={inPool}
                          onCheckedChange={(v) => setPickerSelected((prev) => (v ? [...prev, p.id] : prev.filter((id) => id !== p.id)))}
                        />
                        <span className="min-w-0 flex-1 truncate font-mono">{p.address}</span>
                        <span className="text-muted-foreground font-mono">{p.protocol}</span>
                        <ProxyStatus status={p.status} />
                        {inPool && <span className="text-muted-foreground">in pool</span>}
                      </label>
                    )
                  })}
                </div>
              )}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPickerOpen(false)} disabled={addingProxies}>Cancel</Button>
            <Button onClick={handleAddProxiesToPool} disabled={addingProxies || pickerSelected.length === 0}>
              {addingProxies ? "Adding…" : `Add ${pickerSelected.length || ""}`.trim()}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Alert rule */}
      <Dialog open={alertDialogOpen} onOpenChange={setAlertDialogOpen}>
        <DialogContent>
          <form onSubmit={handleSaveAlertRule} className="space-y-4">
            <DialogHeader>
              <DialogTitle>{editAlertRule ? "Edit alert rule" : "New alert rule"}</DialogTitle>
              <DialogDescription>Fires a webhook when the pool&apos;s active count drops below the threshold, at most once per cooldown.</DialogDescription>
            </DialogHeader>
            <div className="space-y-1.5">
              <Label htmlFor="rule-url">Webhook URL</Label>
              <Input id="rule-url" className="font-mono" placeholder="https://hooks.slack.com/…" required value={alertForm.webhook_url} onChange={(e) => setAlertForm({ ...alertForm, webhook_url: e.target.value })} />
              <p className="text-muted-foreground text-[0.6875rem] leading-4">
                Any URL receives the JSON payload. For a Telegram topic use the bot&apos;s sendMessage endpoint — the text is generated:{" "}
                <span className="font-mono break-all">https://api.telegram.org/bot&lt;TOKEN&gt;/sendMessage?chat_id=&lt;-100…&gt;&amp;message_thread_id=&lt;topic&gt;</span>
              </p>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="rule-min">Fire below (active proxies)</Label>
                <Input id="rule-min" type="number" min={1} value={alertForm.min_active_proxies} onChange={(e) => setAlertForm({ ...alertForm, min_active_proxies: parseInt(e.target.value) || 1 })} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="rule-cooldown">Cooldown (minutes)</Label>
                <Input id="rule-cooldown" type="number" min={1} value={alertForm.cooldown_minutes} onChange={(e) => setAlertForm({ ...alertForm, cooldown_minutes: parseInt(e.target.value) || 30 })} />
              </div>
            </div>
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="rule-enabled" className="text-foreground">Enabled</Label>
              <Switch id="rule-enabled" checked={alertForm.enabled} onCheckedChange={(v) => setAlertForm({ ...alertForm, enabled: v })} />
            </div>
            <p className="text-muted-foreground font-mono text-[0.6875rem] leading-4">
              {'{ event: "pool.degraded", pool_id, pool_name, active_proxies, total_proxies, threshold, fired_at }'}
            </p>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setAlertDialogOpen(false)}>Cancel</Button>
              <Button type="submit" disabled={savingAlert}>{savingAlert ? "Saving…" : editAlertRule ? "Save" : "Create rule"}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete pool “{deleteTarget?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>Users with this pool as main or fallback skip it from their next request. Proxies stay in the inventory.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={confirmDelete}>Delete</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={deleteRuleId !== null} onOpenChange={(o) => !o && setDeleteRuleId(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this alert rule?</AlertDialogTitle>
            <AlertDialogDescription>The webhook stops firing for this pool.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={confirmDeleteRule}>Delete</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

export default function Page() {
  return (
    <Suspense fallback={<LoadingLine />}>
      <PoolsPage />
    </Suspense>
  )
}
