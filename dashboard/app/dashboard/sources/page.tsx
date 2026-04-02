"use client"

import { useEffect, useState, useCallback } from "react"
import {
  Plus, Trash2, RefreshCw, Globe, Clock, CheckCircle2,
  XCircle, Pencil, Download, Loader2, AlertCircle,
} from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { ProxySource, CreateSourceRequest } from "@/lib/types"
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

const PROTOCOLS = ["http", "https", "socks4", "socks4a", "socks5"] as const
const DEFAULT_FORM: CreateSourceRequest = {
  name: "",
  url: "",
  protocol: "http",
  enabled: true,
  interval_minutes: 60,
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

  useEffect(() => { load() }, [load])

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
    })
    setDialogOpen(true)
  }

  const handleSave = async () => {
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
    } catch (e: any) {
      toast.error(e.message || "Failed to save source")
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (id: number) => {
    if (!confirm("Delete this source?")) return
    try {
      await api.deleteSource(id)
      toast.success("Source deleted")
      load()
    } catch {
      toast.error("Failed to delete source")
    }
  }

  const handleFetch = async (id: number) => {
    setFetchingId(id)
    try {
      const res = await api.fetchSourceNow(id)
      toast.success(`Fetched ${res.imported} proxies from source`)
      load()
    } catch (e: any) {
      toast.error(e.message || "Fetch failed")
    } finally {
      setFetchingId(null)
    }
  }

  const handleEnrichGeo = async () => {
    setEnriching(true)
    try {
      const res = await api.enrichGeo()
      toast.success(`Geo enriched ${res.enriched} proxies`)
    } catch {
      toast.error("Geo enrichment failed")
    } finally {
      setEnriching(false)
    }
  }

  const toggleEnabled = async (s: ProxySource) => {
    try {
      await api.updateSource(s.id, { enabled: !s.enabled })
      load()
    } catch {
      toast.error("Failed to toggle source")
    }
  }

  const formatDate = (d?: string) =>
    d ? new Date(d).toLocaleString() : "Never"

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
          <h1 className="text-2xl font-bold">Proxy Sources</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Remote TXT lists of proxies — fetched automatically on schedule
          </p>
        </div>
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={handleEnrichGeo}
            disabled={enriching}
          >
            {enriching
              ? <Loader2 className="h-4 w-4 animate-spin mr-2" />
              : <Globe className="h-4 w-4 mr-2" />}
            Enrich GeoIP
          </Button>
          <Button size="sm" onClick={openCreate}>
            <Plus className="h-4 w-4 mr-2" />
            Add Source
          </Button>
        </div>
      </div>

      {/* Stats cards */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">Total Sources</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{sources.length}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">Enabled</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold text-green-500">
              {sources.filter(s => s.enabled).length}
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">Total Imported</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {sources.reduce((s, x) => s + x.last_count, 0)}
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">With Errors</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold text-red-500">
              {sources.filter(s => s.last_error).length}
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Table */}
      {sources.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-center justify-center py-16 gap-3">
            <Download className="h-10 w-10 text-muted-foreground" />
            <p className="text-muted-foreground">No proxy sources yet. Add one to get started.</p>
            <Button onClick={openCreate}><Plus className="h-4 w-4 mr-2" />Add Source</Button>
          </CardContent>
        </Card>
      ) : (
        <Card>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>URL</TableHead>
                  <TableHead>Protocol</TableHead>
                  <TableHead>Interval</TableHead>
                  <TableHead>Last Fetch</TableHead>
                  <TableHead>Imported</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Enabled</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sources.map(s => (
                  <TableRow key={s.id}>
                    <TableCell className="font-medium">{s.name}</TableCell>
                    <TableCell className="max-w-[200px]">
                      <span className="truncate block text-xs text-muted-foreground" title={s.url}>
                        {s.url}
                      </span>
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline">{s.protocol.toUpperCase()}</Badge>
                    </TableCell>
                    <TableCell>
                      <span className="flex items-center gap-1 text-sm">
                        <Clock className="h-3 w-3" />
                        {s.interval_minutes}m
                      </span>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground whitespace-nowrap">
                      {formatDate(s.last_fetched_at)}
                    </TableCell>
                    <TableCell>
                      <span className="font-semibold">{s.last_count.toLocaleString()}</span>
                    </TableCell>
                    <TableCell>
                      {s.last_error ? (
                        <span className="flex items-center gap-1 text-xs text-red-500" title={s.last_error}>
                          <AlertCircle className="h-3 w-3 flex-shrink-0" />
                          Error
                        </span>
                      ) : s.last_fetched_at ? (
                        <span className="flex items-center gap-1 text-xs text-green-500">
                          <CheckCircle2 className="h-3 w-3" />
                          OK
                        </span>
                      ) : (
                        <span className="text-xs text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <Switch
                        checked={s.enabled}
                        onCheckedChange={() => toggleEnabled(s)}
                      />
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost" size="icon"
                          onClick={() => handleFetch(s.id)}
                          disabled={fetchingId === s.id}
                          title="Fetch now"
                        >
                          {fetchingId === s.id
                            ? <Loader2 className="h-4 w-4 animate-spin" />
                            : <RefreshCw className="h-4 w-4" />}
                        </Button>
                        <Button
                          variant="ghost" size="icon"
                          onClick={() => openEdit(s)}
                          title="Edit"
                        >
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost" size="icon"
                          onClick={() => handleDelete(s.id)}
                          title="Delete"
                          className="text-red-500 hover:text-red-600"
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}

      {/* Add / Edit dialog */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>{editSource ? "Edit Source" : "Add Proxy Source"}</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-4 py-2">
            <div className="flex flex-col gap-1.5">
              <Label>Name</Label>
              <Input
                placeholder="e.g. Public Proxy List"
                value={form.name}
                onChange={e => setForm({ ...form, name: e.target.value })}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label>URL (TXT file with one proxy per line)</Label>
              <Input
                placeholder="https://example.com/proxies.txt"
                value={form.url}
                onChange={e => setForm({ ...form, url: e.target.value })}
              />
              <p className="text-xs text-muted-foreground">
                Format: <code>ip:port</code> one per line. e.g. <code>185.220.101.5:9051</code>
              </p>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label>Protocol</Label>
              <Select
                value={form.protocol}
                onValueChange={v => setForm({ ...form, protocol: v as any })}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PROTOCOLS.map(p => (
                    <SelectItem key={p} value={p}>{p.toUpperCase()}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label>Refresh interval (minutes)</Label>
              <Input
                type="number"
                min={1}
                value={form.interval_minutes}
                onChange={e => setForm({ ...form, interval_minutes: parseInt(e.target.value) || 60 })}
              />
            </div>
            <div className="flex items-center gap-2">
              <Switch
                id="src-enabled"
                checked={form.enabled}
                onCheckedChange={v => setForm({ ...form, enabled: v })}
              />
              <Label htmlFor="src-enabled">Enabled</Label>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>Cancel</Button>
            <Button onClick={handleSave} disabled={saving}>
              {saving && <Loader2 className="h-4 w-4 animate-spin mr-2" />}
              {editSource ? "Save Changes" : "Add Source"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
