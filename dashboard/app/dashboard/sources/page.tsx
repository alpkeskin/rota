"use client"

import { useEffect, useState, useCallback } from "react"
import { ChevronDown } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { ProxySource, CreateSourceRequest } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogFooter } from "@/components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Switch } from "@/components/ui/switch"
import { Label } from "@/components/ui/label"
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
import { PageHeader, Content, EmptyLine, LoadingLine } from "@/components/page-header"
import { StatStrip } from "@/components/stat-strip"
import { ErrorStatus, OkStatus, PendingStatus, RunningStatus } from "@/components/status"
import { count, formatDateTime, humanizeMinutes, relative } from "@/lib/format"

const PROTOCOLS = ["http", "https", "socks4", "socks4a", "socks5"] as const
const DEFAULT_FORM: CreateSourceRequest = {
  name: "",
  url: "",
  protocol: "http",
  enabled: true,
  interval_minutes: 60,
  cleanup_enabled: false,
  cleanup_days: 7,
}

export default function SourcesPage() {
  const [sources, setSources] = useState<ProxySource[]>([])
  const [loading, setLoading] = useState(true)
  const [fetchingId, setFetchingId] = useState<number | null>(null)
  const [enriching, setEnriching] = useState(false)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editSource, setEditSource] = useState<ProxySource | null>(null)
  const [form, setForm] = useState<CreateSourceRequest>(DEFAULT_FORM)
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ProxySource | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await api.getSources()
      setSources(res.sources)
    } catch {
      toast.error("Failed to load sources")
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const openCreate = () => {
    setEditSource(null)
    setForm(DEFAULT_FORM)
    setDialogOpen(true)
  }

  const openEdit = (s: ProxySource) => {
    setEditSource(s)
    setForm({
      name: s.name,
      url: s.url,
      protocol: s.protocol,
      enabled: s.enabled,
      interval_minutes: s.interval_minutes,
      cleanup_enabled: s.cleanup_enabled,
      cleanup_days: s.cleanup_days || 7,
    })
    setDialogOpen(true)
  }

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim() || !form.url.trim()) {
      toast.error("Name and URL are required")
      return
    }
    setSaving(true)
    try {
      if (editSource) {
        await api.updateSource(editSource.id, form)
        toast.success("Source updated")
      } else {
        await api.createSource(form)
        toast.success("Source created")
      }
      setDialogOpen(false)
      load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to save source")
    } finally {
      setSaving(false)
    }
  }

  const confirmDelete = async () => {
    if (!deleteTarget) return
    try {
      await api.deleteSource(deleteTarget.id)
      toast.success("Source deleted")
      load()
    } catch {
      toast.error("Failed to delete source")
    } finally {
      setDeleteTarget(null)
    }
  }

  const handleFetch = async (id: number) => {
    setFetchingId(id)
    try {
      const res = await api.fetchSourceNow(id)
      toast.success(`Imported ${res.imported} new proxies`)
      load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Fetch failed")
    } finally {
      setFetchingId(null)
    }
  }

  const handleEnrichGeo = async () => {
    setEnriching(true)
    try {
      const res = await api.enrichGeo()
      toast.success(`GeoIP resolved for ${res.enriched} proxies`)
    } catch {
      toast.error("GeoIP enrichment failed")
    } finally {
      setEnriching(false)
    }
  }

  const toggleEnabled = async (s: ProxySource) => {
    try {
      await api.updateSource(s.id, { enabled: !s.enabled })
      load()
    } catch {
      toast.error("Failed to update source")
    }
  }

  const enabled = sources.filter((s) => s.enabled).length
  const withErrors = sources.filter((s) => s.last_error).length
  const imported = sources.reduce((s, x) => s + x.last_count, 0)
  const returned = sources.reduce((s, x) => s + x.last_total, 0)

  return (
    <>
      <PageHeader
        title="Sources"
        description="Remote text lists fetched on a schedule. Each fetch adds proxies that are new to the inventory; existing ones are left untouched."
      >
        <Button variant="outline" onClick={handleEnrichGeo} disabled={enriching}>
          {enriching ? "Resolving GeoIP…" : "Resolve GeoIP"}
        </Button>
        <Button onClick={openCreate}>Add source</Button>
      </PageHeader>

      <StatStrip
        columns={4}
        stats={[
          { label: "Sources", value: count(sources.length), hint: `${count(enabled)} enabled` },
          { label: "Lines on last fetch", value: count(returned), hint: "summed across sources" },
          { label: "New on last fetch", value: count(imported), hint: "proxies not seen before" },
          {
            label: "Failing",
            value: count(withErrors),
            hint: withErrors ? "last fetch returned an error" : "every last fetch succeeded",
            tone: withErrors > 0 ? "critical" : sources.length > 0 ? "good" : "default",
          },
        ]}
      />

      <Content>
        {loading ? (
          <LoadingLine />
        ) : sources.length === 0 ? (
          <EmptyLine>No sources yet. Add a URL that serves one ip:port per line.</EmptyLine>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>URL</TableHead>
                <TableHead>Protocol</TableHead>
                <TableHead>Every</TableHead>
                <TableHead>Last fetch</TableHead>
                <TableHead className="text-right" title="Lines returned by the source on the last fetch">Lines</TableHead>
                <TableHead className="text-right" title="Proxies created on the last fetch">New</TableHead>
                <TableHead>Result</TableHead>
                <TableHead title="Delete proxies missing from this source for this many days">Cleanup</TableHead>
                <TableHead>Enabled</TableHead>
                <TableHead className="w-8" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {sources.map((s) => (
                <TableRow key={s.id}>
                  <TableCell className="font-medium">{s.name}</TableCell>
                  <TableCell className="text-muted-foreground max-w-[16rem] truncate font-mono" title={s.url}>
                    {s.url}
                  </TableCell>
                  <TableCell className="text-muted-foreground font-mono">{s.protocol}</TableCell>
                  <TableCell className="num">{humanizeMinutes(s.interval_minutes)}</TableCell>
                  <TableCell className="text-muted-foreground" title={s.last_fetched_at ? formatDateTime(s.last_fetched_at) : undefined}>
                    {s.last_fetched_at ? relative(s.last_fetched_at) : "never"}
                  </TableCell>
                  <TableCell className="num text-right">{count(s.last_total)}</TableCell>
                  <TableCell className="num text-right font-medium">{count(s.last_count)}</TableCell>
                  <TableCell>
                    {fetchingId === s.id ? (
                      <RunningStatus>Fetching</RunningStatus>
                    ) : s.last_error ? (
                      <span title={s.last_error}>
                        <ErrorStatus>Error</ErrorStatus>
                      </span>
                    ) : s.last_fetched_at ? (
                      <OkStatus />
                    ) : (
                      <PendingStatus>Not fetched</PendingStatus>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground">{s.cleanup_enabled ? `after ${s.cleanup_days}d` : "—"}</TableCell>
                  <TableCell>
                    <Switch checked={s.enabled} onCheckedChange={() => toggleEnabled(s)} aria-label={`${s.name} enabled`} />
                  </TableCell>
                  <TableCell className="text-right">
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${s.name}`}>
                          <ChevronDown aria-hidden />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => handleFetch(s.id)} disabled={fetchingId === s.id}>Fetch now</DropdownMenuItem>
                        <DropdownMenuItem onClick={() => openEdit(s)}>Edit</DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem variant="destructive" onClick={() => setDeleteTarget(s)}>Delete</DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {withErrors > 0 && (
          <div className="mt-6 space-y-1">
            <p className="label">Last errors</p>
            {sources
              .filter((s) => s.last_error)
              .map((s) => (
                <p key={s.id} className="text-muted-foreground">
                  <span className="text-foreground font-medium">{s.name}</span>
                  <span className="ml-2 font-mono">{s.last_error}</span>
                </p>
              ))}
          </div>
        )}
      </Content>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <form onSubmit={handleSave} className="space-y-4">
            <DialogHeader>
              <DialogTitle>{editSource ? "Edit source" : "Add source"}</DialogTitle>
              <DialogDescription>
                {editSource ? "Changes apply from the next scheduled fetch." : "The first fetch runs on the next scheduler tick, or now via “Fetch now”."}
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-1.5">
              <Label htmlFor="src-name">Name</Label>
              <Input id="src-name" placeholder="Public HTTP list" required value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="src-url">URL</Label>
              <Input id="src-url" className="font-mono" placeholder="https://example.com/proxies.txt" required value={form.url} onChange={(e) => setForm({ ...form, url: e.target.value })} />
              <p className="text-muted-foreground text-[0.6875rem] leading-4">Plain text, one ip:port per line.</p>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="src-protocol">Protocol</Label>
                <Select value={form.protocol} onValueChange={(v) => setForm({ ...form, protocol: v as CreateSourceRequest["protocol"] })}>
                  <SelectTrigger id="src-protocol" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {PROTOCOLS.map((p) => (
                      <SelectItem key={p} value={p}>{p.toUpperCase()}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="src-interval">Fetch every (minutes)</Label>
                <Input id="src-interval" type="number" min={1} value={form.interval_minutes} onChange={(e) => setForm({ ...form, interval_minutes: parseInt(e.target.value) || 60 })} />
              </div>
            </div>
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="src-enabled" className="text-foreground">Enabled</Label>
              <Switch id="src-enabled" checked={form.enabled} onCheckedChange={(v) => setForm({ ...form, enabled: v })} />
            </div>
            <div className="border-border space-y-3 border-t pt-4">
              <div className="flex items-start justify-between gap-4">
                <div>
                  <Label htmlFor="src-cleanup" className="text-foreground">Remove stale proxies</Label>
                  <p className="text-muted-foreground mt-1 text-[0.6875rem] leading-4">
                    Delete proxies that this source stopped listing for longer than the threshold. Proxies still in the response are never deleted.
                  </p>
                </div>
                <Switch id="src-cleanup" checked={!!form.cleanup_enabled} onCheckedChange={(v) => setForm({ ...form, cleanup_enabled: v })} />
              </div>
              {form.cleanup_enabled && (
                <div className="space-y-1.5">
                  <Label htmlFor="src-cleanup-days">Delete after (days)</Label>
                  <Input
                    id="src-cleanup-days"
                    type="number"
                    min={1}
                    max={365}
                    value={form.cleanup_days ?? 7}
                    onChange={(e) => setForm({ ...form, cleanup_days: Math.max(1, Math.min(365, parseInt(e.target.value) || 7)) })}
                  />
                </div>
              )}
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setDialogOpen(false)}>Cancel</Button>
              <Button type="submit" disabled={saving}>{saving ? "Saving…" : editSource ? "Save changes" : "Add source"}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete “{deleteTarget?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>Scheduled fetches stop. Proxies it already imported stay in the inventory.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={confirmDelete}>Delete</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
