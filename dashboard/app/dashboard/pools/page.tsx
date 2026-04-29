"use client"

import { useEffect, useState, useCallback, useRef } from "react"
import {
  Plus, Trash2, RefreshCw,
  Pencil, Loader2, Layers, ShieldCheck, Globe,
  Download, Bell, BellOff, Tag,
} from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import {
  ProxyPool, PoolProxy, GeoSummaryItem, HCJob, CreatePoolRequest,
  PoolAlertRule, CreatePoolAlertRuleRequest,
} from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter,
} from "@/components/ui/dialog"
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select"
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table"
import { Switch } from "@/components/ui/switch"
import { Label } from "@/components/ui/label"
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card"
import { Progress } from "@/components/ui/progress"
import { GeoSelector } from "@/components/geo-selector"

// ────────────────────────────────────────────────────────────────────────────
// Types & helpers
// ────────────────────────────────────────────────────────────────────────────

const ROTATION_LABELS: Record<string, string> = {
  roundrobin: "Round Robin",
  random: "Random",
  stick: "Sticky (N requests)",
}

const FLAG_CDN = (cc: string) =>
  `https://flagcdn.com/16x12/${cc.toLowerCase()}.png`

const statusColor = (s: string) =>
  s === "active" ? "text-green-500" : s === "failed" ? "text-red-500" : "text-yellow-500"

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

// ────────────────────────────────────────────────────────────────────────────
// Main page
// ────────────────────────────────────────────────────────────────────────────

export default function PoolsPage() {
  const [pools, setPools] = useState<ProxyPool[]>([])
  const [geoCountries, setGeoCountries] = useState<GeoSummaryItem[]>([])
  const [loading, setLoading] = useState(true)
  const [activeTab, setActiveTab] = useState<"pools" | "geo">("pools")


  // Pool detail panel
  const [selectedPool, setSelectedPool] = useState<ProxyPool | null>(null)
  const [poolProxies, setPoolProxies] = useState<PoolProxy[]>([])
  const [poolProxiesLoading, setPoolProxiesLoading] = useState(false)
  const [hcJob, setHcJob] = useState<HCJob | null>(null)
  const [hcRunning, setHcRunning] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const hcPollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  // Dialog
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editPool, setEditPool] = useState<ProxyPool | null>(null)
  const [form, setForm] = useState<CreatePoolRequest>(DEFAULT_POOL_FORM)
  const [saving, setSaving] = useState(false)

  // Quick-add geo filter inside edit dialog
  const [newGeoCountry, setNewGeoCountry] = useState("")
  const [newGeoCity, setNewGeoCity] = useState("")

  // Alert rules
  const [alertRules, setAlertRules] = useState<PoolAlertRule[]>([])
  const [alertDialogOpen, setAlertDialogOpen] = useState(false)
  const [alertForm, setAlertForm] = useState<CreatePoolAlertRuleRequest>({
    enabled: true, min_active_proxies: 5, webhook_url: "", webhook_method: "POST", cooldown_minutes: 30,
  })
  const [editAlertRule, setEditAlertRule] = useState<PoolAlertRule | null>(null)
  const [savingAlert, setSavingAlert] = useState(false)

  const loadAll = useCallback(async () => {
    setLoading(true)
    try {
      const [poolsRes, countriesRes] = await Promise.all([
        api.getPools(),
        api.getGeoByCountry(),
      ])
      setPools(poolsRes.pools)
      setGeoCountries(countriesRes.geo)
    } catch {
      toast.error("Failed to load pools data")
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { loadAll() }, [loadAll])

  const openCreate = () => {
    setEditPool(null)
    setForm({ ...DEFAULT_POOL_FORM, geo_filters: [] })
    setNewGeoCountry("")
    setNewGeoCity("")
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
    setDialogOpen(true)
  }

  const handleSave = async () => {
    if (!form.name.trim()) { toast.error("Name is required"); return }
    setSaving(true)
    try {
      if (editPool) {
        await api.updatePool(editPool.id, form)
        toast.success("Pool updated")
      } else {
        await api.createPool(form)
        toast.success("Pool created")
      }
      setDialogOpen(false)
      loadAll()
    } catch (e: any) {
      toast.error(e.message || "Failed to save pool")
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (id: number) => {
    if (!confirm("Delete this pool?")) return
    try {
      await api.deletePool(id)
      toast.success("Pool deleted")
      if (selectedPool?.id === id) setSelectedPool(null)
      loadAll()
    } catch {
      toast.error("Failed to delete pool")
    }
  }

  const handleSelectPool = async (pool: ProxyPool) => {
    setSelectedPool(pool)
    setHcJob(null)
    setPoolProxiesLoading(true)
    try {
      const [proxiesRes, rules] = await Promise.all([
        api.getPoolProxies(pool.id),
        api.getAlertRules(pool.id).catch(() => []),
      ])
      setPoolProxies(proxiesRes.proxies)
      setAlertRules(rules)
    } catch {
      toast.error("Failed to load pool proxies")
    } finally {
      setPoolProxiesLoading(false)
    }
  }

  const handleExport = (format: "txt" | "csv") => {
    if (!selectedPool) return
    const url = api.getPoolExportUrl(selectedPool.id, format)
    window.open(url, "_blank")
  }

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

  const handleSaveAlertRule = async () => {
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
      const rules = await api.getAlertRules(selectedPool.id)
      setAlertRules(rules)
    } catch {
      toast.error("Failed to save alert rule")
    } finally {
      setSavingAlert(false)
    }
  }

  const handleDeleteAlertRule = async (ruleId: number) => {
    if (!selectedPool || !confirm("Delete this alert rule?")) return
    try {
      await api.deleteAlertRule(selectedPool.id, ruleId)
      setAlertRules(prev => prev.filter(r => r.id !== ruleId))
      toast.success("Alert rule deleted")
    } catch {
      toast.error("Failed to delete alert rule")
    }
  }

  const handleSync = async () => {
    if (!selectedPool) return
    setSyncing(true)
    try {
      const res = await api.syncPool(selectedPool.id)
      toast.success(`Synced ${res.synced} proxies into pool`)
      handleSelectPool(selectedPool)
      loadAll()
    } catch {
      toast.error("Sync failed")
    } finally {
      setSyncing(false)
    }
  }

  const stopHcPoll = useCallback(() => {
    if (hcPollRef.current) {
      clearInterval(hcPollRef.current)
      hcPollRef.current = null
    }
  }, [])

  const handleHealthCheck = async () => {
    if (!selectedPool) return
    setHcRunning(true)
    setHcJob(null)
    stopHcPoll()
    try {
      const res = await api.healthCheckPool(selectedPool.id, selectedPool.health_check_url, 20)
      // Start polling job status
      const poolId = selectedPool.id
      hcPollRef.current = setInterval(async () => {
        try {
          const job = await api.getHealthCheckJob(poolId, res.job_id)
          setHcJob(job)
          if (job.status === "done" || job.status === "failed") {
            stopHcPoll()
            setHcRunning(false)
            if (job.status === "done") {
              toast.success(`Health check done: ${job.active}/${job.progress} active`)
            } else {
              toast.error(`Health check failed: ${job.error}`)
            }
            if (selectedPool) handleSelectPool(selectedPool)
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

  // Cleanup on unmount
  useEffect(() => () => stopHcPoll(), [stopHcPoll])

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">Proxy Pools</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Named groups of proxies with geo filters and independent rotation strategies
          </p>
        </div>
        <Button size="sm" onClick={openCreate}>
          <Plus className="h-4 w-4 mr-2" />
          New Pool
        </Button>
      </div>

      {/* Custom tab bar */}
      <div className="flex gap-1 border-b pb-0">
        <button
          className={`flex items-center gap-2 px-4 py-2 text-sm font-medium border-b-2 transition-colors ${activeTab === "pools" ? "border-primary text-primary" : "border-transparent text-muted-foreground hover:text-foreground"}`}
          onClick={() => setActiveTab("pools")}
        >
          <Layers className="h-4 w-4" />Pools
        </button>
        <button
          className={`flex items-center gap-2 px-4 py-2 text-sm font-medium border-b-2 transition-colors ${activeTab === "geo" ? "border-primary text-primary" : "border-transparent text-muted-foreground hover:text-foreground"}`}
          onClick={() => setActiveTab("geo")}
        >
          <Globe className="h-4 w-4" />Geo Distribution
        </button>
      </div>

        {/* ── Pools tab ─────────���─────────────────────────────��───────────── */}
        {activeTab === "pools" && <div className="mt-4">
          {pools.length === 0 ? (
            <Card>
              <CardContent className="flex flex-col items-center justify-center py-16 gap-3">
                <Layers className="h-10 w-10 text-muted-foreground" />
                <p className="text-muted-foreground">No pools yet. Create one to get started.</p>
                <Button onClick={openCreate}><Plus className="h-4 w-4 mr-2" />New Pool</Button>
              </CardContent>
            </Card>
          ) : (
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              {/* Pool list */}
              <div className="flex flex-col gap-2">
                {pools.map(pool => (
                  <Card
                    key={pool.id}
                    className={`cursor-pointer transition-colors hover:border-primary/60 ${selectedPool?.id === pool.id ? "border-primary" : ""}`}
                    onClick={() => handleSelectPool(pool)}
                  >
                    <CardContent className="p-4">
                      <div className="flex items-start justify-between gap-2">
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2 flex-wrap">
                            <span className="font-semibold truncate">{pool.name}</span>
                            {!pool.enabled && <Badge variant="secondary">Disabled</Badge>}
                            <Badge variant="outline" className="text-xs">
                              {ROTATION_LABELS[pool.rotation_method] || pool.rotation_method}
                            </Badge>
                          </div>
                          {pool.description && (
                            <p className="text-xs text-muted-foreground mt-0.5 truncate">{pool.description}</p>
                          )}
                          <div className="flex items-center gap-3 mt-2 text-xs text-muted-foreground">
                            {pool.country_code && (
                              <span className="flex items-center gap-1">
                                <img src={FLAG_CDN(pool.country_code)} alt={pool.country_code} className="h-3" />
                                {pool.country_code}
                              </span>
                            )}
                            {pool.region_name && <span>{pool.region_name}</span>}
                            {pool.city_name && <span>{pool.city_name}</span>}
                          </div>
                        </div>
                        <div className="flex flex-col items-end gap-1 text-xs shrink-0">
                          <span className="font-bold text-base">{pool.total_proxies}</span>
                          <span className="text-muted-foreground">total</span>
                          <div className="flex gap-1">
                            <span className="text-green-500">{pool.active_proxies} ✓</span>
                            <span className="text-red-500">{pool.failed_proxies} ✗</span>
                          </div>
                        </div>
                      </div>
                      {pool.total_proxies > 0 && (
                        <Progress
                          className="h-1 mt-2"
                          value={(pool.active_proxies / pool.total_proxies) * 100}
                        />
                      )}
                    </CardContent>
                  </Card>
                ))}
              </div>

              {/* Pool detail */}
              {selectedPool ? (
                <div className="flex flex-col gap-3">
                  <Card>
                    <CardHeader className="pb-3">
                      <div className="flex items-center justify-between">
                        <CardTitle className="text-base">{selectedPool.name}</CardTitle>
                        <div className="flex gap-1">
                          <Button
                            variant="outline" size="sm"
                            onClick={handleSync}
                            disabled={syncing}
                            title="Re-sync proxies from geo filters"
                          >
                            {syncing
                              ? <Loader2 className="h-3 w-3 animate-spin mr-1" />
                              : <RefreshCw className="h-3 w-3 mr-1" />}
                            Sync
                          </Button>
                          <Button
                            variant="outline" size="sm"
                            onClick={handleHealthCheck}
                            disabled={hcRunning}
                            title="Run health check on all proxies in pool"
                          >
                            {hcRunning
                              ? <Loader2 className="h-3 w-3 animate-spin mr-1" />
                              : <ShieldCheck className="h-3 w-3 mr-1" />}
                            Check
                          </Button>
                          <Button
                            variant="outline" size="sm"
                            onClick={() => handleExport("txt")}
                            title="Export as TXT"
                          >
                            <Download className="h-3 w-3 mr-1" />
                            TXT
                          </Button>
                          <Button
                            variant="outline" size="sm"
                            onClick={() => handleExport("csv")}
                            title="Export as CSV"
                          >
                            <Download className="h-3 w-3 mr-1" />
                            CSV
                          </Button>
                          <Button
                            variant="ghost" size="icon"
                            onClick={() => openEdit(selectedPool)}
                          >
                            <Pencil className="h-3 w-3" />
                          </Button>
                          <Button
                            variant="ghost" size="icon"
                            className="text-red-500"
                            onClick={() => handleDelete(selectedPool.id)}
                          >
                            <Trash2 className="h-3 w-3" />
                          </Button>
                        </div>
                      </div>
                      <CardDescription className="text-xs space-y-0.5">
                        <div className="flex items-center gap-2 flex-wrap">
                          <span>Rotation: <strong>{ROTATION_LABELS[selectedPool.rotation_method]}</strong>
                            {selectedPool.rotation_method === "stick" && ` (every ${selectedPool.stick_count} req)`}
                          </span>
                          <Badge variant={selectedPool.sync_mode === "manual" ? "secondary" : "outline"} className="text-xs">
                            {selectedPool.sync_mode === "manual" ? "manual sync" : "auto sync"}
                          </Badge>
                        </div>
                        <div>Health check: <code className="text-xs bg-muted px-1 rounded">{selectedPool.health_check_cron}</code>
                          {" → "}<span className="text-muted-foreground">{selectedPool.health_check_url}</span>
                        </div>
                        {(selectedPool.isp_filters?.length ?? 0) > 0 && (
                          <div className="flex gap-1 flex-wrap">
                            <span className="text-muted-foreground">ISP:</span>
                            {selectedPool.isp_filters!.map(i => <Badge key={i} variant="outline" className="text-xs">{i}</Badge>)}
                          </div>
                        )}
                        {(selectedPool.tag_filters?.length ?? 0) > 0 && (
                          <div className="flex gap-1 flex-wrap">
                            <Tag className="h-3 w-3 text-muted-foreground" />
                            {selectedPool.tag_filters!.map(t => <Badge key={t} variant="secondary" className="text-xs">{t}</Badge>)}
                          </div>
                        )}
                      </CardDescription>
                    </CardHeader>
                    {hcJob && (
                      <CardContent className="pt-0 pb-3">
                        <div className="rounded-md bg-muted p-3 text-xs space-y-2">
                          {/* Progress bar */}
                          {(hcJob.status === "running" || hcJob.status === "pending") && hcJob.total > 0 && (
                            <div>
                              <div className="flex justify-between mb-1">
                                <span className="text-muted-foreground">
                                  Checking {hcJob.progress}/{hcJob.total}…
                                </span>
                                <span className="text-muted-foreground">
                                  {Math.round((hcJob.progress / hcJob.total) * 100)}%
                                </span>
                              </div>
                              <Progress value={(hcJob.progress / hcJob.total) * 100} className="h-1.5" />
                            </div>
                          )}
                          <div className="flex gap-4 flex-wrap">
                            <span>Checked: <strong>{hcJob.progress}</strong>{hcJob.total > 0 && `/${hcJob.total}`}</span>
                            <span className="text-green-500">Active: <strong>{hcJob.active}</strong></span>
                            <span className="text-red-500">Failed: <strong>{hcJob.failed}</strong></span>
                            {hcJob.status === "running" && (
                              <span className="flex items-center gap-1 text-blue-500">
                                <Loader2 className="h-3 w-3 animate-spin" />running
                              </span>
                            )}
                            {hcJob.status === "done" && hcJob.finished_at && (
                              <span className="text-muted-foreground">
                                Done in {Math.round((new Date(hcJob.finished_at).getTime() - new Date(hcJob.started_at).getTime()) / 1000)}s
                              </span>
                            )}
                            {hcJob.status === "failed" && (
                              <span className="text-red-500">{hcJob.error}</span>
                            )}
                          </div>
                        </div>
                      </CardContent>
                    )}
                  </Card>

                  {/* Proxies in pool */}
                  <Card>
                    <CardHeader className="pb-2">
                      <CardTitle className="text-sm">
                        Proxies in pool ({poolProxies.length})
                      </CardTitle>
                    </CardHeader>
                    <CardContent className="p-0">
                      {poolProxiesLoading ? (
                        <div className="flex justify-center py-8">
                          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                        </div>
                      ) : poolProxies.length === 0 ? (
                        <p className="text-center py-6 text-sm text-muted-foreground">
                          No proxies. Use Sync to populate from geo filters.
                        </p>
                      ) : (
                        <div className="max-h-80 overflow-auto">
                          <Table>
                            <TableHeader>
                              <TableRow>
                                <TableHead className="text-xs">Address</TableHead>
                                <TableHead className="text-xs">Geo</TableHead>
                                <TableHead className="text-xs">Status</TableHead>
                                <TableHead className="text-xs text-right">RT</TableHead>
                              </TableRow>
                            </TableHeader>
                            <TableBody>
                              {poolProxies.map(pp => (
                                <TableRow key={pp.proxy_id}>
                                  <TableCell className="text-xs font-mono">{pp.address}</TableCell>
                                  <TableCell className="text-xs">
                                    <span className="flex items-center gap-1">
                                      {pp.country_code && (
                                        <img src={FLAG_CDN(pp.country_code)} alt={pp.country_code} className="h-3" />
                                      )}
                                      {pp.city_name || pp.country_name || "—"}
                                    </span>
                                  </TableCell>
                                  <TableCell className="text-xs">
                                    <span className={statusColor(pp.status)}>
                                      {pp.status}
                                    </span>
                                  </TableCell>
                                  <TableCell className="text-xs text-right text-muted-foreground">
                                    {pp.avg_response_time ? `${pp.avg_response_time}ms` : "—"}
                                  </TableCell>
                                </TableRow>
                              ))}
                            </TableBody>
                          </Table>
                        </div>
                      )}
                    </CardContent>
                  </Card>

                  {/* Alert Rules */}
                  <Card>
                    <CardHeader className="pb-2">
                      <div className="flex items-center justify-between">
                        <CardTitle className="text-sm flex items-center gap-1">
                          <Bell className="h-3.5 w-3.5" />
                          Alert Rules ({alertRules.length})
                        </CardTitle>
                        <Button size="sm" variant="outline" onClick={openCreateAlertRule}>
                          <Plus className="h-3 w-3 mr-1" />Add Rule
                        </Button>
                      </div>
                    </CardHeader>
                    <CardContent className="p-0">
                      {alertRules.length === 0 ? (
                        <p className="text-center py-4 text-xs text-muted-foreground">
                          No alert rules. Add one to get notified when proxy count drops.
                        </p>
                      ) : (
                        <div className="divide-y">
                          {alertRules.map(rule => (
                            <div key={rule.id} className="flex items-center justify-between px-4 py-2 text-xs">
                              <div className="flex items-center gap-2">
                                {rule.enabled
                                  ? <Bell className="h-3 w-3 text-yellow-500" />
                                  : <BellOff className="h-3 w-3 text-muted-foreground" />
                                }
                                <span className="font-mono truncate max-w-[160px]" title={rule.webhook_url}>
                                  {rule.webhook_url}
                                </span>
                                <Badge variant="outline" className="text-xs">
                                  &lt; {rule.min_active_proxies} active
                                </Badge>
                              </div>
                              <div className="flex gap-1">
                                <Button variant="ghost" size="icon" className="h-6 w-6"
                                  onClick={() => openEditAlertRule(rule)}>
                                  <Pencil className="h-3 w-3" />
                                </Button>
                                <Button variant="ghost" size="icon" className="h-6 w-6 text-red-500"
                                  onClick={() => handleDeleteAlertRule(rule.id)}>
                                  <Trash2 className="h-3 w-3" />
                                </Button>
                              </div>
                            </div>
                          ))}
                        </div>
                      )}
                    </CardContent>
                  </Card>
                </div>
              ) : (
                <Card className="flex items-center justify-center min-h-[200px]">
                  <p className="text-muted-foreground text-sm">← Select a pool to view details</p>
                </Card>
              )}
            </div>
          )}
        </div>}

        {/* ── Geo distribution tab ─────────────────────────────────────────── */}
        {activeTab === "geo" && (
          <div className="mt-4">
            <GeoSelector
              countries={geoCountries}
              existingPools={pools}
              onCreated={loadAll}
            />
          </div>
        )}

      {/* ── Create / Edit pool dialog ──────────────────────────────────────── */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-lg max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{editPool ? "Edit Pool" : "Create Proxy Pool"}</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-4 py-2">
            <div className="grid grid-cols-2 gap-3">
              <div className="col-span-2 flex flex-col gap-1.5">
                <Label>Name</Label>
                <Input
                  placeholder="e.g. US East"
                  value={form.name}
                  onChange={e => setForm({ ...form, name: e.target.value })}
                />
              </div>
              <div className="col-span-2 flex flex-col gap-1.5">
                <Label>Description</Label>
                <Input
                  placeholder="Optional description"
                  value={form.description}
                  onChange={e => setForm({ ...form, description: e.target.value })}
                />
              </div>

              {/* Multi-country geo filters — lets a pool match proxies from multiple countries */}
              <div className="col-span-2 flex flex-col gap-2 border rounded-md p-3">
                <div className="flex items-center justify-between">
                  <Label className="text-sm font-medium">Countries in this pool</Label>
                  <span className="text-xs text-muted-foreground">
                    {form.geo_filters?.length ?? 0} filter{(form.geo_filters?.length ?? 0) === 1 ? "" : "s"}
                  </span>
                </div>

                {/* Existing filters as removable badges */}
                <div className="flex flex-wrap gap-1.5 min-h-[28px]">
                  {(form.geo_filters ?? []).length === 0 && (
                    <span className="text-xs text-muted-foreground italic">
                      No country filters yet — this pool will not sync any proxies. Add at least one below.
                    </span>
                  )}
                  {(form.geo_filters ?? []).map((f, idx) => (
                    <Badge key={`${f.country_code}-${f.city_name ?? ""}-${idx}`} variant="secondary" className="gap-1 pr-1">
                      <img src={FLAG_CDN(f.country_code)} alt={f.country_code} className="h-3" />
                      <span className="font-mono text-xs">{f.country_code.toUpperCase()}</span>
                      {f.city_name && <span className="text-xs text-muted-foreground">/ {f.city_name}</span>}
                      <button
                        type="button"
                        className="ml-0.5 rounded hover:bg-muted-foreground/20 px-1 text-xs leading-none"
                        onClick={() => setForm({
                          ...form,
                          geo_filters: (form.geo_filters ?? []).filter((_, i) => i !== idx),
                        })}
                        title="Remove"
                      >
                        ×
                      </button>
                    </Badge>
                  ))}
                </div>

                {/* Quick-add row: country code + optional city + Add button */}
                <div className="flex gap-2 items-end pt-1">
                  <div className="flex flex-col gap-1 flex-1">
                    <Label htmlFor="new-geo-cc" className="text-xs">Country</Label>
                    <Input
                      id="new-geo-cc"
                      placeholder="BR"
                      maxLength={3}
                      value={newGeoCountry}
                      onChange={e => setNewGeoCountry(e.target.value.toUpperCase())}
                      onKeyDown={e => {
                        if (e.key === "Enter") {
                          e.preventDefault()
                          const cc = newGeoCountry.trim().toUpperCase()
                          if (!cc) return
                          const existing = form.geo_filters ?? []
                          if (existing.some(f => f.country_code === cc && (f.city_name ?? "") === newGeoCity.trim())) {
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
                      }}
                    />
                  </div>
                  <div className="flex flex-col gap-1 flex-[2]">
                    <Label htmlFor="new-geo-city" className="text-xs">City (optional)</Label>
                    <Input
                      id="new-geo-city"
                      placeholder="Leave empty for whole country"
                      value={newGeoCity}
                      onChange={e => setNewGeoCity(e.target.value)}
                    />
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    onClick={() => {
                      const cc = newGeoCountry.trim().toUpperCase()
                      if (!cc) { toast.error("Enter a country code"); return }
                      const existing = form.geo_filters ?? []
                      if (existing.some(f => f.country_code === cc && (f.city_name ?? "") === newGeoCity.trim())) {
                        toast.error("Already added")
                        return
                      }
                      setForm({
                        ...form,
                        geo_filters: [...existing, { country_code: cc, ...(newGeoCity.trim() ? { city_name: newGeoCity.trim() } : {}) }],
                      })
                      setNewGeoCountry("")
                      setNewGeoCity("")
                    }}
                  >
                    <Plus className="h-4 w-4 mr-1" /> Add
                  </Button>
                </div>

                {/* Quick-pick common countries from existing proxy inventory */}
                {geoCountries.length > 0 && (
                  <div className="pt-2 border-t mt-1 flex flex-col gap-1.5">
                    <Label className="text-xs text-muted-foreground">Quick pick from inventory</Label>
                    <div className="flex flex-wrap gap-1">
                      {geoCountries.slice(0, 12).map(gc => {
                        const already = (form.geo_filters ?? []).some(f => f.country_code === gc.country_code && !f.city_name)
                        return (
                          <Button
                            key={gc.country_code}
                            type="button"
                            size="sm"
                            variant={already ? "secondary" : "outline"}
                            disabled={already}
                            className="h-6 px-2 text-xs"
                            onClick={() => setForm({
                              ...form,
                              geo_filters: [...(form.geo_filters ?? []), { country_code: gc.country_code }],
                            })}
                            title={`${gc.country_name ?? gc.country_code} — ${gc.total} proxies`}
                          >
                            <img src={FLAG_CDN(gc.country_code)} alt={gc.country_code} className="h-3 mr-1" />
                            {gc.country_code}
                            <span className="ml-1 text-muted-foreground">({gc.total})</span>
                          </Button>
                        )
                      })}
                    </div>
                  </div>
                )}
              </div>

              <div className="flex flex-col gap-1.5">
                <Label>Rotation strategy</Label>
                <Select
                  value={form.rotation_method}
                  onValueChange={v => setForm({ ...form, rotation_method: v as any })}
                >
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="roundrobin">Round Robin</SelectItem>
                    <SelectItem value="random">Random</SelectItem>
                    <SelectItem value="stick">Sticky (N requests)</SelectItem>
                  </SelectContent>
                </Select>
              </div>

              {form.rotation_method === "stick" && (
                <div className="flex flex-col gap-1.5">
                  <Label>Stick requests count</Label>
                  <Input
                    type="number"
                    min={1}
                    value={form.stick_count}
                    onChange={e => setForm({ ...form, stick_count: parseInt(e.target.value) || 10 })}
                  />
                </div>
              )}

              <div className="col-span-2 flex flex-col gap-1.5">
                <Label>Health check URL</Label>
                <Input
                  placeholder="https://api.ipify.org"
                  value={form.health_check_url}
                  onChange={e => setForm({ ...form, health_check_url: e.target.value })}
                />
              </div>
              <div className="col-span-2 flex flex-col gap-1.5">
                <Label>Health check cron</Label>
                <Input
                  placeholder="*/30 * * * *"
                  value={form.health_check_cron}
                  onChange={e => setForm({ ...form, health_check_cron: e.target.value })}
                />
                <p className="text-xs text-muted-foreground">
                  e.g. <code>*/30 * * * *</code> = every 30 min
                </p>
              </div>
            </div>

            <div className="flex flex-col gap-2">
              <div className="flex items-center gap-2">
                <Switch
                  id="hc-enabled"
                  checked={form.health_check_enabled}
                  onCheckedChange={v => setForm({ ...form, health_check_enabled: v })}
                />
                <Label htmlFor="hc-enabled">Auto health check</Label>
              </div>
              <div className="flex items-center gap-2">
                <Switch
                  id="auto-sync"
                  checked={form.auto_sync}
                  onCheckedChange={v => setForm({ ...form, auto_sync: v })}
                />
                <Label htmlFor="auto-sync">Auto-sync membership on import</Label>
              </div>
              <div className="flex items-center gap-2">
                <Switch
                  id="sync-manual"
                  checked={form.sync_mode === "manual"}
                  onCheckedChange={v => setForm({ ...form, sync_mode: v ? "manual" : "auto" })}
                />
                <Label htmlFor="sync-manual">Manual sync mode (don&apos;t auto-rebuild on import)</Label>
              </div>
              <div className="flex items-center gap-2">
                <Switch
                  id="pool-enabled"
                  checked={form.enabled}
                  onCheckedChange={v => setForm({ ...form, enabled: v })}
                />
                <Label htmlFor="pool-enabled">Enabled</Label>
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>Cancel</Button>
            <Button onClick={handleSave} disabled={saving}>
              {saving && <Loader2 className="h-4 w-4 animate-spin mr-2" />}
              {editPool ? "Save Changes" : "Create Pool"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Alert Rule Dialog */}
      <Dialog open={alertDialogOpen} onOpenChange={setAlertDialogOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>{editAlertRule ? "Edit Alert Rule" : "New Alert Rule"}</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <div>
              <Label className="text-xs">Webhook URL</Label>
              <Input
                placeholder="https://hooks.slack.com/..."
                value={alertForm.webhook_url}
                onChange={e => setAlertForm({ ...alertForm, webhook_url: e.target.value })}
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <Label className="text-xs">Min Active Proxies</Label>
                <Input
                  type="number" min={1}
                  value={alertForm.min_active_proxies}
                  onChange={e => setAlertForm({ ...alertForm, min_active_proxies: parseInt(e.target.value) || 1 })}
                />
                <p className="text-xs text-muted-foreground mt-1">Fire when active drops below this</p>
              </div>
              <div>
                <Label className="text-xs">Cooldown (minutes)</Label>
                <Input
                  type="number" min={1}
                  value={alertForm.cooldown_minutes}
                  onChange={e => setAlertForm({ ...alertForm, cooldown_minutes: parseInt(e.target.value) || 30 })}
                />
              </div>
            </div>
            <div className="flex items-center gap-2">
              <Switch
                checked={alertForm.enabled}
                onCheckedChange={v => setAlertForm({ ...alertForm, enabled: v })}
              />
              <Label className="text-xs">Enabled</Label>
            </div>
            <div className="rounded-md bg-muted p-3 text-xs text-muted-foreground">
              <strong>Payload sent:</strong><br />
              {"{ event: \"pool.degraded\", pool_id, pool_name, active_proxies, total_proxies, threshold, fired_at }"}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAlertDialogOpen(false)}>Cancel</Button>
            <Button onClick={handleSaveAlertRule} disabled={savingAlert}>
              {savingAlert && <Loader2 className="h-4 w-4 animate-spin mr-2" />}
              {editAlertRule ? "Save" : "Create Rule"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

    </div>
  )
}
