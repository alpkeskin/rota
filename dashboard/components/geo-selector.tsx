"use client"

import { useState, useCallback } from "react"
import {
  ChevronRight, ChevronDown, Plus, Loader2, Layers,
  Globe, MapPin, CheckSquare, Square,
} from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { GeoSummaryItem, GeoCityItem, GeoFilter, ProxyPool } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Progress } from "@/components/ui/progress"
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter,
} from "@/components/ui/dialog"
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Label } from "@/components/ui/label"

// ─────────────────────────────────────────────────────────────────────────────

interface SelectedFilter {
  key: string          // "CC" or "CC::city"
  label: string
  filter: GeoFilter
}

interface Props {
  countries: GeoSummaryItem[]
  existingPools: ProxyPool[]
  onCreated: () => void
}

const FLAG = (cc: string) =>
  `https://flagcdn.com/20x15/${cc.toLowerCase()}.png`

// ─────────────────────────────────────────────────────────────────────────────

export function GeoSelector({ countries, existingPools, onCreated }: Props) {
  const [search, setSearch] = useState("")
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [cities, setCities] = useState<Record<string, GeoCityItem[]>>({})
  const [loadingCities, setLoadingCities] = useState<Set<string>>(new Set())

  // Multi-select
  const [selected, setSelected] = useState<Map<string, SelectedFilter>>(new Map())

  // Create pool dialog
  const [createOpen, setCreateOpen] = useState(false)
  const [poolName, setPoolName] = useState("")
  const [rotation, setRotation] = useState("roundrobin")
  const [stickCount, setStickCount] = useState(10)
  const [hcUrl, setHcUrl] = useState("https://api.ipify.org")
  const [hcCron, setHcCron] = useState("*/30 * * * *")
  const [autoSync, setAutoSync] = useState(true)
  const [saving, setSaving] = useState(false)

  // ── helpers ────────────────────────────────────────────────────────────────

  const countryKey = (cc: string) => cc
  const cityKey = (cc: string, city: string) => `${cc}::${city}`

  const isSelected = (key: string) => selected.has(key)

  const toggle = (key: string, label: string, filter: GeoFilter) => {
    setSelected(prev => {
      const next = new Map(prev)
      if (next.has(key)) next.delete(key)
      else next.set(key, { key, label, filter })
      return next
    })
  }

  const selectAll = () => {
    const next = new Map<string, SelectedFilter>()
    filtered.forEach(g => {
      const key = countryKey(g.country_code)
      next.set(key, {
        key,
        label: `${g.country_name} (${g.country_code})`,
        filter: { country_code: g.country_code },
      })
    })
    setSelected(next)
  }

  const clearAll = () => setSelected(new Map())

  // ── expand / load cities ──────────────────────────────────────────────────

  const toggleExpand = useCallback(async (cc: string) => {
    if (expanded.has(cc)) {
      setExpanded(prev => { const s = new Set(prev); s.delete(cc); return s })
      return
    }
    setExpanded(prev => new Set(prev).add(cc))
    if (!cities[cc]) {
      setLoadingCities(prev => new Set(prev).add(cc))
      try {
        const res = await api.getGeoCities(cc)
        setCities(prev => ({ ...prev, [cc]: res.cities }))
      } catch {
        toast.error(`Failed to load cities for ${cc}`)
      } finally {
        setLoadingCities(prev => { const s = new Set(prev); s.delete(cc); return s })
      }
    }
  }, [expanded, cities])

  // ── create pool ───────────────────────────────────────────────────────────

  const openCreate = () => {
    if (selected.size === 0) { toast.error("Select at least one location"); return }
    // Auto-generate name from selection
    const sel = Array.from(selected.values())
    if (sel.length === 1) {
      setPoolName(sel[0].label)
    } else {
      const countries = [...new Set(sel.map(s => s.filter.country_code))].join(", ")
      setPoolName(`Mixed: ${countries}`)
    }
    setCreateOpen(true)
  }

  const handleCreate = async () => {
    if (!poolName.trim()) { toast.error("Pool name required"); return }
    setSaving(true)
    try {
      const filters: GeoFilter[] = Array.from(selected.values()).map(s => s.filter)
      await api.createPool({
        name: poolName,
        geo_filters: filters,
        rotation_method: rotation as any,
        stick_count: stickCount,
        health_check_url: hcUrl,
        health_check_cron: hcCron,
        health_check_enabled: true,
        auto_sync: autoSync,
        enabled: true,
      })
      toast.success(`Pool "${poolName}" created and synced`)
      setCreateOpen(false)
      setSelected(new Map())
      onCreated()
    } catch (e: any) {
      toast.error(e.message || "Failed to create pool")
    } finally {
      setSaving(false)
    }
  }

  // ── filter ────────────────────────────────────────────────────────────────

  const filtered = countries.filter(g =>
    !search ||
    g.country_name.toLowerCase().includes(search.toLowerCase()) ||
    g.country_code.toLowerCase().includes(search.toLowerCase())
  )

  const totalProxies = countries.reduce((s, g) => s + g.total, 0)
  const totalActive  = countries.reduce((s, g) => s + g.active, 0)

  const hasCountryPool = (cc: string) =>
    existingPools.some(p => p.country_code === cc && !p.city_name)

  // ─────────────────────────────────────────────────────────────────────────

  if (countries.length === 0) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-16 gap-3">
          <Globe className="h-10 w-10 text-muted-foreground" />
          <p className="text-muted-foreground">No geo data yet.</p>
          <p className="text-xs text-muted-foreground">
            Import proxies via Sources, then click "Enrich GeoIP".
          </p>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      {/* Stats */}
      <div className="grid grid-cols-3 gap-3">
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">Countries</CardTitle></CardHeader>
          <CardContent><div className="text-2xl font-bold">{countries.length}</div></CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">Total Proxies</CardTitle></CardHeader>
          <CardContent><div className="text-2xl font-bold">{totalProxies.toLocaleString()}</div></CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">Active</CardTitle></CardHeader>
          <CardContent><div className="text-2xl font-bold text-green-500">{totalActive.toLocaleString()}</div></CardContent>
        </Card>
      </div>

      {/* Toolbar */}
      <div className="flex items-center gap-2 flex-wrap">
        <div className="relative flex-1 min-w-48">
          <Globe className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder="Search country…"
            value={search}
            onChange={e => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        {selected.size > 0 ? (
          <>
            <Badge variant="secondary" className="shrink-0">
              {selected.size} selected
            </Badge>
            <Button size="sm" onClick={openCreate}>
              <Plus className="h-4 w-4 mr-1" />
              Create Pool from selection
            </Button>
            <Button size="sm" variant="ghost" onClick={clearAll}>Clear</Button>
          </>
        ) : (
          <Button size="sm" variant="outline" onClick={selectAll}>
            <CheckSquare className="h-4 w-4 mr-1" />
            Select all
          </Button>
        )}
      </div>

      {/* Selected summary chips */}
      {selected.size > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {Array.from(selected.values()).map(s => (
            <Badge
              key={s.key}
              variant="outline"
              className="cursor-pointer hover:bg-destructive/10"
              onClick={() => toggle(s.key, s.label, s.filter)}
            >
              {s.label} ×
            </Badge>
          ))}
        </div>
      )}

      {/* Country list */}
      <Card>
        <CardContent className="p-0 divide-y">
          {filtered.map(g => {
            const cc = g.country_code
            const ck = countryKey(cc)
            const isExp = expanded.has(cc)
            const citiesLoading = loadingCities.has(cc)
            const citiesList = cities[cc] || []
            const pct = g.total > 0 ? Math.round((g.active / g.total) * 100) : 0
            const poolExists = hasCountryPool(cc)

            return (
              <div key={cc}>
                {/* Country row */}
                <div className={`flex items-center gap-2 px-4 py-2.5 hover:bg-muted/40 transition-colors ${isSelected(ck) ? "bg-primary/5" : ""}`}>
                  {/* Checkbox */}
                  <Checkbox
                    checked={isSelected(ck)}
                    onCheckedChange={() => toggle(ck, `${g.country_name} (${cc})`, { country_code: cc })}
                  />

                  {/* Expand toggle */}
                  <button
                    className="flex items-center gap-2 flex-1 text-left min-w-0"
                    onClick={() => toggleExpand(cc)}
                  >
                    <span className="text-muted-foreground w-4 flex-shrink-0">
                      {citiesLoading
                        ? <Loader2 className="h-3 w-3 animate-spin" />
                        : isExp
                          ? <ChevronDown className="h-3 w-3" />
                          : <ChevronRight className="h-3 w-3" />}
                    </span>
                    {cc !== "??" && (
                      <img src={FLAG(cc)} alt={cc} className="h-3.5 rounded-sm flex-shrink-0" />
                    )}
                    <span className="font-medium text-sm truncate">{g.country_name}</span>
                    <span className="text-xs text-muted-foreground font-mono flex-shrink-0">{cc}</span>
                  </button>

                  {/* Stats */}
                  <div className="flex items-center gap-3 flex-shrink-0">
                    <div className="flex items-center gap-2 w-28 hidden sm:flex">
                      <div className="flex-1 bg-muted rounded-full h-1.5 overflow-hidden">
                        <div className="h-full bg-green-500 rounded-full" style={{ width: `${pct}%` }} />
                      </div>
                      <span className="text-xs text-muted-foreground w-8 text-right">{pct}%</span>
                    </div>
                    <span className="text-sm font-semibold w-14 text-right">{g.total.toLocaleString()}</span>
                    <span className="text-sm text-green-500 w-10 text-right">{g.active}</span>
                    <div className="w-24 flex justify-end">
                      {poolExists ? (
                        <span className="text-xs text-muted-foreground flex items-center gap-1">
                          <Layers className="h-3 w-3" />pool
                        </span>
                      ) : (
                        <Button
                          size="sm"
                          variant="outline"
                          className="h-6 text-xs px-2"
                          onClick={e => {
                            e.stopPropagation()
                            setSelected(new Map([[ck, { key: ck, label: `${g.country_name} (${cc})`, filter: { country_code: cc } }]]))
                            setPoolName(`${g.country_name} (${cc})`)
                            setCreateOpen(true)
                          }}
                        >
                          <Plus className="h-3 w-3 mr-0.5" />Pool
                        </Button>
                      )}
                    </div>
                  </div>
                </div>

                {/* City rows */}
                {isExp && (
                  <div className="bg-muted/20">
                    {citiesLoading ? (
                      <div className="flex justify-center py-4">
                        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
                      </div>
                    ) : citiesList.length === 0 ? (
                      <p className="text-xs text-muted-foreground text-center py-3">No city data</p>
                    ) : (
                      citiesList.map(city => {
                        const ck2 = cityKey(cc, city.city_name)
                        const cityPct = city.total > 0 ? Math.round((city.active / city.total) * 100) : 0
                        const cityPoolExists = existingPools.some(
                          p => p.country_code === cc && p.city_name === city.city_name
                        )
                        return (
                          <div
                            key={ck2}
                            className={`flex items-center gap-2 pl-10 pr-4 py-2 border-t border-muted hover:bg-muted/40 transition-colors ${isSelected(ck2) ? "bg-primary/5" : ""}`}
                          >
                            <Checkbox
                              checked={isSelected(ck2)}
                              onCheckedChange={() => toggle(
                                ck2,
                                `${city.city_name}, ${cc}`,
                                { country_code: cc, city_name: city.city_name }
                              )}
                            />
                            <MapPin className="h-3 w-3 text-muted-foreground flex-shrink-0" />
                            <span className="flex-1 text-sm truncate">{city.city_name}</span>
                            <span className="text-xs text-muted-foreground hidden sm:inline">{city.region_name}</span>
                            <div className="flex items-center gap-3 flex-shrink-0">
                              <div className="flex items-center gap-2 w-28 hidden sm:flex">
                                <div className="flex-1 bg-muted rounded-full h-1.5 overflow-hidden">
                                  <div className="h-full bg-green-500 rounded-full" style={{ width: `${cityPct}%` }} />
                                </div>
                                <span className="text-xs text-muted-foreground w-8 text-right">{cityPct}%</span>
                              </div>
                              <span className="text-sm font-semibold w-14 text-right">{city.total.toLocaleString()}</span>
                              <span className="text-sm text-green-500 w-10 text-right">{city.active}</span>
                              <div className="w-24 flex justify-end">
                                {cityPoolExists ? (
                                  <span className="text-xs text-muted-foreground flex items-center gap-1">
                                    <Layers className="h-3 w-3" />pool
                                  </span>
                                ) : (
                                  <Button
                                    size="sm"
                                    variant="outline"
                                    className="h-6 text-xs px-2"
                                    onClick={e => {
                                      e.stopPropagation()
                                      const key = ck2
                                      setSelected(new Map([[key, {
                                        key,
                                        label: `${city.city_name}, ${cc}`,
                                        filter: { country_code: cc, city_name: city.city_name }
                                      }]]))
                                      setPoolName(`${city.city_name}, ${cc}`)
                                      setCreateOpen(true)
                                    }}
                                  >
                                    <Plus className="h-3 w-3 mr-0.5" />Pool
                                  </Button>
                                )}
                              </div>
                            </div>
                          </div>
                        )
                      })
                    )}
                  </div>
                )}
              </div>
            )
          })}
        </CardContent>
      </Card>

      {/* Create pool dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>Create Pool</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-4 py-2">
            {/* Selection summary */}
            <div className="bg-muted rounded-md p-3 text-xs space-y-1">
              <div className="font-medium text-sm mb-1">Geo filters ({selected.size})</div>
              <div className="flex flex-wrap gap-1 max-h-24 overflow-y-auto">
                {Array.from(selected.values()).map(s => (
                  <Badge key={s.key} variant="secondary">{s.label}</Badge>
                ))}
              </div>
              <p className="text-muted-foreground">
                Pool will include all proxies from the above locations. Auto-syncs when new proxies are imported.
              </p>
            </div>

            <div className="flex flex-col gap-1.5">
              <Label>Pool name</Label>
              <Input value={poolName} onChange={e => setPoolName(e.target.value)} placeholder="e.g. US + UK" />
            </div>

            <div className="flex flex-col gap-1.5">
              <Label>Rotation</Label>
              <Select value={rotation} onValueChange={setRotation}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="roundrobin">Round Robin</SelectItem>
                  <SelectItem value="random">Random</SelectItem>
                  <SelectItem value="stick">Sticky (N requests)</SelectItem>
                </SelectContent>
              </Select>
            </div>

            {rotation === "stick" && (
              <div className="flex flex-col gap-1.5">
                <Label>Requests per IP</Label>
                <Input type="number" min={1} value={stickCount} onChange={e => setStickCount(+e.target.value || 10)} />
              </div>
            )}

            <div className="grid grid-cols-2 gap-3">
              <div className="flex flex-col gap-1.5">
                <Label>Health check URL</Label>
                <Input value={hcUrl} onChange={e => setHcUrl(e.target.value)} />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label>Cron</Label>
                <Input value={hcCron} onChange={e => setHcCron(e.target.value)} placeholder="*/30 * * * *" />
              </div>
            </div>

            <div className="flex items-center gap-2">
              <Switch id="auto-sync-dlg" checked={autoSync} onCheckedChange={setAutoSync} />
              <Label htmlFor="auto-sync-dlg">Auto-sync when proxies are imported</Label>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>Cancel</Button>
            <Button onClick={handleCreate} disabled={saving}>
              {saving && <Loader2 className="h-4 w-4 animate-spin mr-2" />}
              Create &amp; Sync Pool
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
