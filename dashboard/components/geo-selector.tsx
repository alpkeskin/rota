"use client"

import { useState, useCallback } from "react"
import { ChevronRight, ChevronDown, Loader2 } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { GeoSummaryItem, GeoCityItem, GeoFilter, ProxyPool, CreatePoolRequest } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogFooter } from "@/components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Label } from "@/components/ui/label"
import { SearchInput } from "@/components/controls"
import { StatStrip } from "@/components/stat-strip"
import { Tag } from "@/components/status"
import { UsageBar } from "@/components/usage-bar"
import { EmptyLine, Section } from "@/components/page-header"
import { count } from "@/lib/format"
import { cn } from "@/lib/utils"

interface SelectedFilter {
  key: string // "CC" or "CC::city"
  label: string
  filter: GeoFilter
}

interface Props {
  countries: GeoSummaryItem[]
  existingPools: ProxyPool[]
  onCreated: () => void
}

const FLAG = (cc: string) => `https://flagcdn.com/16x12/${cc.toLowerCase()}.png`

export function GeoSelector({ countries, existingPools, onCreated }: Props) {
  const [search, setSearch] = useState("")
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [cities, setCities] = useState<Record<string, GeoCityItem[]>>({})
  const [loadingCities, setLoadingCities] = useState<Set<string>>(new Set())
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

  const cityKey = (cc: string, city: string) => `${cc}::${city}`
  const isSelected = (key: string) => selected.has(key)

  const toggle = (key: string, label: string, filter: GeoFilter) => {
    setSelected((prev) => {
      const next = new Map(prev)
      if (next.has(key)) next.delete(key)
      else next.set(key, { key, label, filter })
      return next
    })
  }

  const filtered = countries.filter(
    (g) => !search || g.country_name.toLowerCase().includes(search.toLowerCase()) || g.country_code.toLowerCase().includes(search.toLowerCase())
  )

  const selectAll = () => {
    const next = new Map<string, SelectedFilter>()
    filtered.forEach((g) => {
      next.set(g.country_code, { key: g.country_code, label: `${g.country_name} (${g.country_code})`, filter: { country_code: g.country_code } })
    })
    setSelected(next)
  }

  const toggleExpand = useCallback(
    async (cc: string) => {
      if (expanded.has(cc)) {
        setExpanded((prev) => {
          const s = new Set(prev)
          s.delete(cc)
          return s
        })
        return
      }
      setExpanded((prev) => new Set(prev).add(cc))
      if (!cities[cc]) {
        setLoadingCities((prev) => new Set(prev).add(cc))
        try {
          const res = await api.getGeoCities(cc)
          setCities((prev) => ({ ...prev, [cc]: res.cities }))
        } catch {
          toast.error(`Failed to load cities for ${cc}`)
        } finally {
          setLoadingCities((prev) => {
            const s = new Set(prev)
            s.delete(cc)
            return s
          })
        }
      }
    },
    [expanded, cities]
  )

  const openCreateFrom = (entries: SelectedFilter[]) => {
    if (entries.length === 0) {
      toast.error("Select at least one location")
      return
    }
    setSelected(new Map(entries.map((e) => [e.key, e])))
    if (entries.length === 1) setPoolName(entries[0].label)
    else setPoolName(`Mixed: ${[...new Set(entries.map((s) => s.filter.country_code))].join(", ")}`)
    setCreateOpen(true)
  }

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!poolName.trim()) {
      toast.error("Pool name required")
      return
    }
    setSaving(true)
    try {
      await api.createPool({
        name: poolName,
        geo_filters: Array.from(selected.values()).map((s) => s.filter),
        rotation_method: rotation as CreatePoolRequest["rotation_method"],
        stick_count: stickCount,
        health_check_url: hcUrl,
        health_check_cron: hcCron,
        health_check_enabled: true,
        auto_sync: autoSync,
        enabled: true,
      })
      toast.success(`Pool “${poolName}” created and synced`)
      setCreateOpen(false)
      setSelected(new Map())
      onCreated()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to create pool")
    } finally {
      setSaving(false)
    }
  }

  const totalProxies = countries.reduce((s, g) => s + g.total, 0)
  const totalActive = countries.reduce((s, g) => s + g.active, 0)
  const hasCountryPool = (cc: string) => existingPools.some((p) => p.country_code === cc && !p.city_name)

  if (countries.length === 0) {
    return <EmptyLine>No geo data yet — import proxies, then run “Resolve GeoIP” on the Sources page.</EmptyLine>
  }

  const row = "grid grid-cols-[auto_1fr_auto] items-center gap-3 px-4 py-2 md:px-6"
  const numbers = "num flex items-center gap-4 text-right"

  return (
    <>
      <StatStrip
        columns={3}
        stats={[
          { label: "Countries", value: count(countries.length), hint: "with at least one proxy" },
          { label: "Proxies with geo", value: count(totalProxies) },
          { label: "Active", value: count(totalActive), hint: totalProxies ? `${Math.round((totalActive / totalProxies) * 100)}% of located proxies` : undefined },
        ]}
      />

      <div className="border-border flex flex-wrap items-center gap-2 border-b px-4 py-3 md:px-6">
        <SearchInput value={search} onChange={setSearch} placeholder="Search country" delay={100} aria-label="Search countries" />
        {selected.size > 0 ? (
          <>
            <span className="num text-muted-foreground ml-2">{selected.size} selected</span>
            <Button size="sm" onClick={() => openCreateFrom(Array.from(selected.values()))}>
              Create pool from selection
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setSelected(new Map())}>
              Clear
            </Button>
          </>
        ) : (
          <Button size="sm" variant="outline" onClick={selectAll}>
            Select all shown
          </Button>
        )}
      </div>

      <div className="border-border text-muted-foreground grid grid-cols-[auto_1fr_auto] items-center gap-3 border-b px-4 py-2 md:px-6">
        <span className="w-4" />
        <span className="label">Location</span>
        <span className={cn(numbers, "label")}>
          <span className="hidden w-24 sm:inline">Usable</span>
          <span className="w-14">Proxies</span>
          <span className="w-12">Active</span>
          <span className="w-16" />
        </span>
      </div>

      <ol>
        {filtered.map((g) => {
          const cc = g.country_code
          const isExp = expanded.has(cc)
          const citiesLoading = loadingCities.has(cc)
          const citiesList = cities[cc] || []
          const pct = g.total > 0 ? Math.round((g.active / g.total) * 100) : 0
          const poolExists = hasCountryPool(cc)
          const label = `${g.country_name} (${cc})`

          return (
            // GeoIP can name one code two ways ("Turkey" / "Türkiye"), so the key needs both.
            <li key={`${cc}-${g.country_name}`} className="border-border border-b">
              <div className={cn(row, "hover:bg-muted/40", isSelected(cc) && "bg-accent/60")}>
                <Checkbox checked={isSelected(cc)} onCheckedChange={() => toggle(cc, label, { country_code: cc })} aria-label={`Select ${label}`} />
                <button type="button" className="flex min-w-0 items-center gap-2 text-left" onClick={() => toggleExpand(cc)} aria-expanded={isExp}>
                  <span className="text-muted-foreground w-3.5 shrink-0">
                    {citiesLoading ? <Loader2 className="size-3 animate-spin" /> : isExp ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
                  </span>
                  {cc !== "??" && (
                    // eslint-disable-next-line @next/next/no-img-element
                    <img src={FLAG(cc)} alt="" width={16} height={12} className="shrink-0" />
                  )}
                  <span className="truncate font-medium">{g.country_name}</span>
                  <span className="text-muted-foreground shrink-0 font-mono">{cc}</span>
                </button>
                <span className={numbers}>
                  <span className="hidden w-24 items-center gap-2 sm:inline-flex">
                    <UsageBar value={pct} className="w-14" />
                    <span className="text-muted-foreground w-8">{pct}%</span>
                  </span>
                  <span className="w-14 font-medium">{count(g.total)}</span>
                  <span className="w-12">{count(g.active)}</span>
                  <span className="inline-flex w-16 justify-end">
                    {poolExists ? (
                      <Tag>has pool</Tag>
                    ) : (
                      <button
                        type="button"
                        className="text-muted-foreground hover:text-foreground font-medium"
                        onClick={() => openCreateFrom([{ key: cc, label, filter: { country_code: cc } }])}
                      >
                        + pool
                      </button>
                    )}
                  </span>
                </span>
              </div>

              {isExp && (
                <ol className="bg-muted/20 border-border border-t">
                  {citiesLoading ? (
                    <li className="text-muted-foreground px-4 py-3 pl-12 md:px-6 md:pl-14">Loading cities…</li>
                  ) : citiesList.length === 0 ? (
                    <li className="text-muted-foreground px-4 py-3 pl-12 md:px-6 md:pl-14">No city-level data for this country.</li>
                  ) : (
                    citiesList.map((city) => {
                      const key = cityKey(cc, city.city_name)
                      const cityPct = city.total > 0 ? Math.round((city.active / city.total) * 100) : 0
                      const cityPoolExists = existingPools.some((p) => p.country_code === cc && p.city_name === city.city_name)
                      const cityLabel = `${city.city_name}, ${cc}`
                      return (
                        <li key={key} className={cn(row, "border-border hover:bg-muted/40 border-b pl-12 last:border-b-0 md:pl-14", isSelected(key) && "bg-accent/60")}>
                          <Checkbox checked={isSelected(key)} onCheckedChange={() => toggle(key, cityLabel, { country_code: cc, city_name: city.city_name })} aria-label={`Select ${cityLabel}`} />
                          <span className="flex min-w-0 items-center gap-2">
                            <span className="truncate">{city.city_name}</span>
                            <span className="text-muted-foreground hidden truncate sm:inline">{city.region_name}</span>
                          </span>
                          <span className={numbers}>
                            <span className="hidden w-24 items-center gap-2 sm:inline-flex">
                              <UsageBar value={cityPct} className="w-14" />
                              <span className="text-muted-foreground w-8">{cityPct}%</span>
                            </span>
                            <span className="w-14 font-medium">{count(city.total)}</span>
                            <span className="w-12">{count(city.active)}</span>
                            <span className="inline-flex w-16 justify-end">
                              {cityPoolExists ? (
                                <Tag>has pool</Tag>
                              ) : (
                                <button
                                  type="button"
                                  className="text-muted-foreground hover:text-foreground font-medium"
                                  onClick={() => openCreateFrom([{ key, label: cityLabel, filter: { country_code: cc, city_name: city.city_name } }])}
                                >
                                  + pool
                                </button>
                              )}
                            </span>
                          </span>
                        </li>
                      )
                    })
                  )}
                </ol>
              )}
            </li>
          )
        })}
      </ol>
      {filtered.length === 0 && <EmptyLine>No country matches “{search}”.</EmptyLine>}

      <Section className="border-b-0">
        <p className="text-muted-foreground">
          Counts are proxies whose GeoIP lookup succeeded; proxies without geo data live in the inventory but never appear here.
        </p>
      </Section>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <form onSubmit={handleCreate} className="space-y-4">
            <DialogHeader>
              <DialogTitle>Create pool</DialogTitle>
              <DialogDescription>The pool takes every proxy in these locations and re-syncs when new proxies are imported.</DialogDescription>
            </DialogHeader>
            <div>
              <p className="label mb-1.5">Locations ({selected.size})</p>
              <div className="flex max-h-24 flex-wrap gap-1 overflow-y-auto">
                {Array.from(selected.values()).map((s) => (
                  <Tag key={s.key}>{s.label}</Tag>
                ))}
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="geo-pool-name">Pool name</Label>
              <Input id="geo-pool-name" value={poolName} onChange={(e) => setPoolName(e.target.value)} required />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="geo-rotation">Rotation</Label>
                <Select value={rotation} onValueChange={setRotation}>
                  <SelectTrigger id="geo-rotation" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="roundrobin">Round robin</SelectItem>
                    <SelectItem value="random">Random</SelectItem>
                    <SelectItem value="stick">Sticky, N requests</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              {rotation === "stick" && (
                <div className="space-y-1.5">
                  <Label htmlFor="geo-stick">Requests per proxy</Label>
                  <Input id="geo-stick" type="number" min={1} value={stickCount} onChange={(e) => setStickCount(+e.target.value || 10)} />
                </div>
              )}
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="geo-hc-url">Health check URL</Label>
                <Input id="geo-hc-url" className="font-mono" value={hcUrl} onChange={(e) => setHcUrl(e.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="geo-hc-cron">Health check cron</Label>
                <Input id="geo-hc-cron" className="font-mono" value={hcCron} onChange={(e) => setHcCron(e.target.value)} placeholder="*/30 * * * *" />
              </div>
            </div>
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="geo-auto-sync" className="text-foreground">Re-sync when proxies are imported</Label>
              <Switch id="geo-auto-sync" checked={autoSync} onCheckedChange={setAutoSync} />
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setCreateOpen(false)}>Cancel</Button>
              <Button type="submit" disabled={saving}>{saving ? "Creating…" : "Create and sync"}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}
