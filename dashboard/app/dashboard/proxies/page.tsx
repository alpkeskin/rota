"use client"

import * as React from "react"
import { Suspense } from "react"
import { ChevronDown, FileText } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
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
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { PageHeader, Content, EmptyLine, LoadingLine } from "@/components/page-header"
import { SearchInput, NativeSelect } from "@/components/controls"
import { SortHeader } from "@/components/sort-header"
import { Pagination } from "@/components/pagination"
import { ProxyStatus, Tag } from "@/components/status"
import { UsageBar } from "@/components/usage-bar"
import { TagInput } from "@/components/tag-input"
import { useUrlState } from "@/hooks/use-url-state"
import { api } from "@/lib/api"
import { Proxy } from "@/lib/types"
import { toast } from "@/lib/toast"
import { count, ms, percent, relative, formatDateTime } from "@/lib/format"
import { cn } from "@/lib/utils"

type Protocol = Proxy["protocol"]
const PROTOCOLS: Protocol[] = ["http", "https", "socks4", "socks4a", "socks5"]
const PAGE_SIZE = 50

// Sort options are named by what they show, not by asc/desc.
const SORTS: { value: string; label: string; sort: string; order: "asc" | "desc" }[] = [
  { value: "newest", label: "Newest first", sort: "created_at", order: "desc" },
  { value: "oldest", label: "Oldest first", sort: "created_at", order: "asc" },
  { value: "most-requests", label: "Most requests", sort: "requests", order: "desc" },
  { value: "slowest", label: "Slowest first", sort: "avg_response_time", order: "desc" },
  { value: "fastest", label: "Fastest first", sort: "avg_response_time", order: "asc" },
  { value: "address", label: "Address A→Z", sort: "address", order: "asc" },
  { value: "status", label: "By status", sort: "status", order: "asc" },
]

const URL_DEFAULTS = { page: "1", q: "", status: "", protocol: "", sort: "created_at", order: "desc" }

function ProtocolSelect({
  value,
  onChange,
  id,
  disabled,
}: {
  value: Protocol
  onChange: (v: Protocol) => void
  id?: string
  disabled?: boolean
}) {
  return (
    <Select value={value} onValueChange={(v) => onChange(v as Protocol)} disabled={disabled}>
      <SelectTrigger id={id} className="w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {PROTOCOLS.map((p) => (
          <SelectItem key={p} value={p}>
            {p.toUpperCase()}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

function ProxiesPage() {
  const [url, setUrl] = useUrlState(URL_DEFAULTS)
  const page = Math.max(1, parseInt(url.page) || 1)
  const order: "asc" | "desc" = url.order === "asc" ? "asc" : "desc"

  const [data, setData] = React.useState<Proxy[]>([])
  const [total, setTotal] = React.useState(0)
  const [isLoading, setIsLoading] = React.useState(true)
  const [selected, setSelected] = React.useState<Set<number>>(new Set())
  const [allTags, setAllTags] = React.useState<string[]>([])

  // Dialogs
  const [addOpen, setAddOpen] = React.useState(false)
  const [editing, setEditing] = React.useState<Proxy | null>(null)
  const [tagOpen, setTagOpen] = React.useState(false)
  const [importOpen, setImportOpen] = React.useState(false)
  const [deleteId, setDeleteId] = React.useState<number | null>(null)
  const [bulkDeleteOpen, setBulkDeleteOpen] = React.useState(false)
  const [deleteAllOpen, setDeleteAllOpen] = React.useState(false)

  const [newProxy, setNewProxy] = React.useState({
    address: "",
    protocol: "http" as Protocol,
    username: "",
    password: "",
    tags: [] as string[],
  })
  const [bulkAddTags, setBulkAddTags] = React.useState<string[]>([])
  const [bulkRemoveTags, setBulkRemoveTags] = React.useState<string[]>([])
  const [isTagging, setIsTagging] = React.useState(false)
  const [isReloading, setIsReloading] = React.useState(false)

  // Import
  const [importFile, setImportFile] = React.useState<File | null>(null)
  const [importProtocol, setImportProtocol] = React.useState<Protocol>("http")
  const [importUsername, setImportUsername] = React.useState("")
  const [importPassword, setImportPassword] = React.useState("")
  const [parsedProxies, setParsedProxies] = React.useState<string[]>([])
  const [isImporting, setIsImporting] = React.useState(false)
  const [importProgress, setImportProgress] = React.useState({ current: 0, total: 0, success: 0, failed: 0, skipped: 0 })
  const [importResults, setImportResults] = React.useState<Array<{ address: string; status: string; error?: string }>>([])
  const [isDragging, setIsDragging] = React.useState(false)

  // Monotonic request counter: only the latest request's result is applied,
  // so a stale response can't push the page indicator back.
  const fetchSeq = React.useRef(0)

  const fetchProxies = React.useCallback(async () => {
    const seq = ++fetchSeq.current
    try {
      setIsLoading(true)
      const response = await api.getProxies({
        page,
        limit: PAGE_SIZE,
        search: url.q || undefined,
        status: url.status || undefined,
        protocol: url.protocol || undefined,
        sort: url.sort,
        order,
      })
      if (seq !== fetchSeq.current) return
      const lastPage = Math.max(1, response.pagination.total_pages)
      if (response.proxies.length === 0 && page > lastPage) {
        setUrl({ page: String(lastPage) }, { replace: true })
        return
      }
      setData(response.proxies)
      setTotal(response.pagination.total)
    } catch (error) {
      if (seq !== fetchSeq.current) return
      console.error("Failed to fetch proxies:", error)
    } finally {
      if (seq === fetchSeq.current) setIsLoading(false)
    }
  }, [page, url.q, url.status, url.protocol, url.sort, order, setUrl])

  React.useEffect(() => {
    fetchProxies()
  }, [fetchProxies])

  const fetchTagList = React.useCallback(async () => {
    try {
      setAllTags(await api.getTagList())
    } catch {
      // Suggestions are best-effort; tagging works without them
    }
  }, [])

  React.useEffect(() => {
    fetchTagList()
  }, [fetchTagList])

  // Selection is per page; clear it when the page's rows change.
  React.useEffect(() => {
    setSelected(new Set())
  }, [page, url.q, url.status, url.protocol, url.sort, order])

  const refresh = () => {
    fetchProxies()
    fetchTagList()
  }

  const setFilter = (patch: Partial<typeof URL_DEFAULTS>) => setUrl({ ...patch, page: "1" }, { replace: true })
  const onSort = (sort: string, ord: "asc" | "desc") => setUrl({ sort, order: ord, page: "1" }, { replace: true })

  // Column headers can produce sort/order pairs the dropdown has no name for;
  // show them as a disabled "Custom" entry so the list stays selectable.
  const sortValue = SORTS.find((s) => s.sort === url.sort && s.order === order)?.value ?? "custom"

  const selectedIds = Array.from(selected)
  const allOnPage = data.length > 0 && data.every((p) => selected.has(p.id))
  const someOnPage = data.some((p) => selected.has(p.id))

  const toggleAll = (on: boolean) => setSelected(on ? new Set(data.map((p) => p.id)) : new Set())
  const toggleOne = (id: number, on: boolean) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (on) next.add(id)
      else next.delete(id)
      return next
    })

  // ── Actions ──────────────────────────────────────────────────────────────

  const handleAddProxy = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      await api.addProxy(newProxy)
      setAddOpen(false)
      setNewProxy({ address: "", protocol: "http", username: "", password: "", tags: [] })
      toast.success("Proxy added")
      refresh()
    } catch (error) {
      toast.error("Failed to add proxy", error instanceof Error ? error.message : "Unknown error")
    }
  }

  const handleEditProxy = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!editing) return
    try {
      await api.updateProxy(editing.id, {
        address: editing.address,
        protocol: editing.protocol,
        username: editing.username,
        tags: editing.tags ?? [],
      })
      setEditing(null)
      toast.success("Proxy updated")
      refresh()
    } catch (error) {
      toast.error("Failed to update proxy", error instanceof Error ? error.message : "Unknown error")
    }
  }

  const handleBulkTag = async (e: React.FormEvent) => {
    e.preventDefault()
    if (selectedIds.length === 0) return
    if (bulkAddTags.length === 0 && bulkRemoveTags.length === 0) {
      toast.error("Nothing to do", "Add or remove at least one tag")
      return
    }
    setIsTagging(true)
    try {
      const res = await api.bulkTagProxies({ ids: selectedIds, add: bulkAddTags, remove: bulkRemoveTags })
      toast.success(`Tags updated on ${res.updated} proxies`)
      setTagOpen(false)
      setBulkAddTags([])
      setBulkRemoveTags([])
      refresh()
    } catch (error) {
      toast.error("Failed to update tags", error instanceof Error ? error.message : "Unknown error")
    } finally {
      setIsTagging(false)
    }
  }

  const confirmDelete = async () => {
    if (deleteId === null) return
    try {
      await api.deleteProxy(deleteId)
      toast.success("Proxy deleted")
      fetchProxies()
    } catch (error) {
      toast.error("Failed to delete proxy", error instanceof Error ? error.message : "Unknown error")
    } finally {
      setDeleteId(null)
    }
  }

  const confirmBulkDelete = async () => {
    try {
      await api.bulkDeleteProxies({ ids: selectedIds })
      setSelected(new Set())
      toast.success(`${selectedIds.length} proxies deleted`)
      fetchProxies()
    } catch (error) {
      toast.error("Failed to delete proxies", error instanceof Error ? error.message : "Unknown error")
    } finally {
      setBulkDeleteOpen(false)
    }
  }

  const confirmDeleteAll = async () => {
    try {
      const res = await api.deleteAllProxies()
      setSelected(new Set())
      toast.success(`${res.deleted} proxies deleted`)
      fetchProxies()
    } catch {
      toast.error("Failed to delete all proxies")
    } finally {
      setDeleteAllOpen(false)
    }
  }

  const handleTestProxy = async (id: number) => {
    try {
      const result = await api.testProxy(id)
      if (result.status === "active") {
        const responseTime = result.response_time || result.duration || 0
        toast.success("Proxy is reachable", `${result.address} answered in ${responseTime} ms`)
      } else {
        toast.error("Proxy test failed", `${result.address} — ${result.error || "Unknown error"}`)
      }
      fetchProxies()
    } catch (error) {
      toast.error("Failed to test proxy", error instanceof Error ? error.message : "Unknown error")
    }
  }

  const handleExport = async (format: "txt" | "json" | "csv") => {
    try {
      const blob = await api.exportProxies(format)
      const href = URL.createObjectURL(blob)
      const a = document.createElement("a")
      a.href = href
      a.download = `proxies.${format}`
      a.click()
      URL.revokeObjectURL(href)
    } catch (error) {
      toast.error("Failed to export proxies", error instanceof Error ? error.message : "Unknown error")
    }
  }

  const handleReloadProxies = async () => {
    try {
      setIsReloading(true)
      await api.reloadProxies()
      toast.success("Rotation pool reloaded", "Every proxy in the database is available for rotation again")
    } catch (error) {
      toast.error("Failed to reload pool", error instanceof Error ? error.message : "Unknown error")
    } finally {
      setIsReloading(false)
    }
  }

  // ── Import ───────────────────────────────────────────────────────────────

  const handleFileUpload = (file: File) => {
    if (!file.name.endsWith(".txt")) {
      toast.error("Invalid file type", "Upload a .txt file")
      return
    }
    const reader = new FileReader()
    reader.onload = (e) => {
      const text = e.target?.result as string
      const lines = text
        .split("\n")
        .map((line) => line.trim())
        .filter((line) => line.length > 0)
        .filter((line) => {
          const parts = line.split(":")
          return parts.length >= 2 && parts[1].match(/^\d+$/)
        })
      setParsedProxies(lines)
      setImportFile(file)
    }
    reader.onerror = () => toast.error("Failed to read file")
    reader.readAsText(file)
  }

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setIsDragging(false)
    const txtFile = Array.from(e.dataTransfer.files).find((f) => f.name.endsWith(".txt"))
    if (txtFile) handleFileUpload(txtFile)
    else toast.error("Invalid file type", "Upload a .txt file")
  }

  const handleImport = async () => {
    if (parsedProxies.length === 0) return
    setIsImporting(true)
    setImportProgress({ current: 0, total: parsedProxies.length, success: 0, failed: 0, skipped: 0 })
    setImportResults([])

    const results: Array<{ address: string; status: string; error?: string }> = []
    let success = 0
    let failed = 0
    let skipped = 0

    for (let i = 0; i < parsedProxies.length; i++) {
      const address = parsedProxies[i]
      try {
        await api.addProxy({
          address,
          protocol: importProtocol,
          username: importUsername || undefined,
          password: importPassword || undefined,
        })
        success++
        results.push({ address, status: "success" })
      } catch (error) {
        const message = error instanceof Error ? error.message : "Unknown error"
        if (message.includes("already exists")) {
          skipped++
          results.push({ address, status: "skipped", error: "Already exists" })
        } else {
          failed++
          results.push({ address, status: "failed", error: message })
        }
      }
      setImportProgress({ current: i + 1, total: parsedProxies.length, success, failed, skipped })
      setImportResults([...results])
    }

    setIsImporting(false)
    setTimeout(refresh, 500)
  }

  const resetImport = () => {
    setImportFile(null)
    setParsedProxies([])
    setImportProtocol("http")
    setImportUsername("")
    setImportPassword("")
    setIsImporting(false)
    setImportProgress({ current: 0, total: 0, success: 0, failed: 0, skipped: 0 })
    setImportResults([])
  }
  const importDone = importProgress.total > 0 && importProgress.current === importProgress.total

  const hasFilters = !!(url.q || url.status || url.protocol)

  // ── Render ───────────────────────────────────────────────────────────────

  return (
    <>
      <PageHeader
        title="Proxies"
        description="Every proxy in the inventory with its last measured health. Filters and sorting are part of the link."
      >
        <Button variant="outline" onClick={handleReloadProxies} disabled={isReloading}>
          {isReloading ? "Reloading…" : "Reload rotation pool"}
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline">
              More <ChevronDown aria-hidden />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => setImportOpen(true)}>Import from .txt</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => handleExport("txt")}>Export as TXT</DropdownMenuItem>
            <DropdownMenuItem onClick={() => handleExport("json")}>Export as JSON</DropdownMenuItem>
            <DropdownMenuItem onClick={() => handleExport("csv")}>Export as CSV</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onClick={() => setDeleteAllOpen(true)}>
              Delete all proxies
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <Button onClick={() => setAddOpen(true)}>Add proxy</Button>
      </PageHeader>

      <div className="border-border flex flex-wrap items-center gap-2 border-b px-4 py-3 md:px-6">
        <SearchInput value={url.q} onChange={(q) => setFilter({ q })} placeholder="Search by address" aria-label="Search proxies" />
        <NativeSelect
          aria-label="Status"
          value={url.status}
          onChange={(status) => setFilter({ status })}
          options={[
            { value: "", label: "All statuses" },
            { value: "active", label: "Active" },
            { value: "failed", label: "Failed" },
            { value: "idle", label: "Idle" },
          ]}
        />
        <NativeSelect
          aria-label="Protocol"
          value={url.protocol}
          onChange={(protocol) => setFilter({ protocol })}
          options={[{ value: "", label: "All protocols" }, ...PROTOCOLS.map((p) => ({ value: p, label: p.toUpperCase() }))]}
        />
        <NativeSelect
          aria-label="Sort"
          value={sortValue}
          onChange={(v) => {
            const s = SORTS.find((x) => x.value === v)
            if (s) onSort(s.sort, s.order)
          }}
          options={[...(sortValue === "custom" ? [{ value: "custom", label: `Custom (${url.sort} ${order})`, disabled: true }] : []), ...SORTS.map((s) => ({ value: s.value, label: s.label }))]}
        />
        {hasFilters && (
          <button
            type="button"
            className="text-muted-foreground hover:text-foreground ml-1 font-medium"
            onClick={() => setFilter({ q: "", status: "", protocol: "" })}
          >
            Clear
          </button>
        )}

        {selectedIds.length > 0 && (
          <div className="ml-auto flex items-center gap-2">
            <span className="num text-muted-foreground">{selectedIds.length} selected</span>
            <Button variant="outline" size="sm" onClick={() => setTagOpen(true)}>
              Edit tags
            </Button>
            <Button variant="destructive" size="sm" onClick={() => setBulkDeleteOpen(true)}>
              Delete
            </Button>
          </div>
        )}
      </div>

      <Content>
        {isLoading && data.length === 0 ? (
          <LoadingLine />
        ) : data.length === 0 ? (
          <EmptyLine>{hasFilters ? "No proxy matches these filters." : "No proxies yet — add one or import a list."}</EmptyLine>
        ) : (
          <Table className={cn(isLoading && "opacity-60")}>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8">
                  <Checkbox
                    checked={allOnPage ? true : someOnPage ? "indeterminate" : false}
                    onCheckedChange={(v) => toggleAll(!!v)}
                    aria-label="Select all on this page"
                  />
                </TableHead>
                <SortHeader field="address" sort={url.sort} order={order} onSort={onSort}>Address</SortHeader>
                <TableHead>Protocol</TableHead>
                <TableHead>Tags</TableHead>
                <SortHeader field="status" sort={url.sort} order={order} onSort={onSort}>Status</SortHeader>
                <SortHeader field="requests" sort={url.sort} order={order} onSort={onSort} align="right">Requests</SortHeader>
                <TableHead className="text-right">Success</TableHead>
                <SortHeader field="avg_response_time" sort={url.sort} order={order} onSort={onSort} align="right">Avg response</SortHeader>
                <TableHead>Last check</TableHead>
                <TableHead className="w-8" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.map((proxy) => {
                const lastCheck = proxy.last_check && proxy.last_check !== "idle" ? proxy.last_check : null
                const tags = proxy.tags ?? []
                return (
                  <TableRow key={proxy.id} data-state={selected.has(proxy.id) ? "selected" : undefined}>
                    <TableCell>
                      <Checkbox checked={selected.has(proxy.id)} onCheckedChange={(v) => toggleOne(proxy.id, !!v)} aria-label={`Select ${proxy.address}`} />
                    </TableCell>
                    <TableCell className="font-mono">
                      {proxy.address}
                      {proxy.username && <span className="text-muted-foreground ml-2 text-[0.6875rem]">auth</span>}
                    </TableCell>
                    <TableCell className="text-muted-foreground font-mono">{proxy.protocol}</TableCell>
                    <TableCell>
                      {tags.length === 0 ? (
                        <span className="text-muted-foreground">—</span>
                      ) : (
                        <span className="flex max-w-[16rem] flex-wrap gap-1">
                          {tags.slice(0, 3).map((t) => (
                            <Tag key={t}>{t}</Tag>
                          ))}
                          {tags.length > 3 && <Tag title={tags.slice(3).join(", ")}>+{tags.length - 3}</Tag>}
                        </span>
                      )}
                    </TableCell>
                    <TableCell>
                      <ProxyStatus status={proxy.status} />
                    </TableCell>
                    <TableCell className="num text-right">{count(proxy.requests)}</TableCell>
                    <TableCell className="num text-right">
                      <span className="inline-flex items-center gap-2">
                        <UsageBar value={proxy.success_rate} className="w-12" />
                        {percent(proxy.success_rate)}
                      </span>
                    </TableCell>
                    <TableCell className="num text-right">{proxy.avg_response_time ? ms(proxy.avg_response_time) : "—"}</TableCell>
                    <TableCell className="text-muted-foreground" title={lastCheck ? formatDateTime(lastCheck) : undefined}>
                      {lastCheck ? relative(lastCheck) : "—"}
                    </TableCell>
                    <TableCell className="text-right">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${proxy.address}`}>
                            <ChevronDown aria-hidden />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onClick={() => navigator.clipboard.writeText(proxy.address)}>Copy address</DropdownMenuItem>
                          <DropdownMenuItem onClick={() => handleTestProxy(proxy.id)}>Test now</DropdownMenuItem>
                          <DropdownMenuItem onClick={() => setEditing(proxy)}>Edit</DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem variant="destructive" onClick={() => setDeleteId(proxy.id)}>
                            Delete
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
        {total > 0 && <Pagination page={page} limit={PAGE_SIZE} total={total} onPage={(p) => setUrl({ page: String(p) })} />}
      </Content>

      {/* Add */}
      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent>
          <form onSubmit={handleAddProxy} className="space-y-4">
            <DialogHeader>
              <DialogTitle>Add proxy</DialogTitle>
              <DialogDescription>The proxy joins the inventory as idle and is picked up by the next health check.</DialogDescription>
            </DialogHeader>
            <div className="space-y-1.5">
              <Label htmlFor="address">Address</Label>
              <Input id="address" placeholder="192.168.1.100:8001" className="font-mono" required value={newProxy.address} onChange={(e) => setNewProxy({ ...newProxy, address: e.target.value })} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="protocol">Protocol</Label>
              <ProtocolSelect id="protocol" value={newProxy.protocol} onChange={(protocol) => setNewProxy({ ...newProxy, protocol })} />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="username">Username (optional)</Label>
                <Input id="username" value={newProxy.username} onChange={(e) => setNewProxy({ ...newProxy, username: e.target.value })} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="password">Password (optional)</Label>
                <Input id="password" type="password" value={newProxy.password} onChange={(e) => setNewProxy({ ...newProxy, password: e.target.value })} />
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="tags">Tags (optional)</Label>
              <TagInput id="tags" value={newProxy.tags} onChange={(tags) => setNewProxy({ ...newProxy, tags })} suggestions={allTags} />
              <p className="text-muted-foreground text-[0.6875rem] leading-4">Tags let pools match proxies that have no GeoIP data, such as local or VPN proxies.</p>
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setAddOpen(false)}>Cancel</Button>
              <Button type="submit">Add proxy</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Edit */}
      <Dialog open={!!editing} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent>
          {editing && (
            <form onSubmit={handleEditProxy} className="space-y-4">
              <DialogHeader>
                <DialogTitle>Edit proxy</DialogTitle>
                <DialogDescription>Changing the address resets nothing else; stats stay attached to this record.</DialogDescription>
              </DialogHeader>
              <div className="space-y-1.5">
                <Label htmlFor="edit-address">Address</Label>
                <Input id="edit-address" className="font-mono" required value={editing.address} onChange={(e) => setEditing({ ...editing, address: e.target.value })} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="edit-protocol">Protocol</Label>
                <ProtocolSelect id="edit-protocol" value={editing.protocol} onChange={(protocol) => setEditing({ ...editing, protocol })} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="edit-username">Username (optional)</Label>
                <Input id="edit-username" value={editing.username || ""} onChange={(e) => setEditing({ ...editing, username: e.target.value })} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="edit-tags">Tags</Label>
                <TagInput id="edit-tags" value={editing.tags ?? []} onChange={(tags) => setEditing({ ...editing, tags })} suggestions={allTags} />
              </div>
              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setEditing(null)}>Cancel</Button>
                <Button type="submit">Save changes</Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>

      {/* Bulk tags */}
      <Dialog
        open={tagOpen}
        onOpenChange={(open) => {
          setTagOpen(open)
          if (!open) {
            setBulkAddTags([])
            setBulkRemoveTags([])
          }
        }}
      >
        <DialogContent>
          <form onSubmit={handleBulkTag} className="space-y-4">
            <DialogHeader>
              <DialogTitle>Edit tags on {selectedIds.length} proxies</DialogTitle>
              <DialogDescription>Added tags are appended to each proxy&apos;s existing tags; removed tags are stripped where present.</DialogDescription>
            </DialogHeader>
            <div className="space-y-1.5">
              <Label htmlFor="bulk-add-tags">Add tags</Label>
              <TagInput id="bulk-add-tags" value={bulkAddTags} onChange={setBulkAddTags} suggestions={allTags} disabled={isTagging} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="bulk-remove-tags">Remove tags</Label>
              <TagInput id="bulk-remove-tags" value={bulkRemoveTags} onChange={setBulkRemoveTags} suggestions={allTags} disabled={isTagging} />
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setTagOpen(false)} disabled={isTagging}>Cancel</Button>
              <Button type="submit" disabled={isTagging}>{isTagging ? "Applying…" : "Apply"}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Import */}
      <Dialog
        open={importOpen}
        onOpenChange={(open) => {
          setImportOpen(open)
          if (!open) resetImport()
        }}
      >
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-[32rem]">
          <DialogHeader>
            <DialogTitle>Import proxies from .txt</DialogTitle>
            <DialogDescription>One proxy per line as ip:port. Lines that do not parse are skipped before anything is sent.</DialogDescription>
          </DialogHeader>

          {!importFile ? (
            <div
              className={cn(
                "border-border grid cursor-pointer place-items-center rounded-md border border-dashed px-6 py-10 text-center transition-colors",
                isDragging ? "bg-accent" : "hover:bg-accent/50"
              )}
              onDragOver={(e) => {
                e.preventDefault()
                setIsDragging(true)
              }}
              onDragLeave={() => setIsDragging(false)}
              onDrop={handleDrop}
              onClick={() => {
                const input = document.createElement("input")
                input.type = "file"
                input.accept = ".txt"
                input.onchange = (e) => {
                  const file = (e.target as HTMLInputElement).files?.[0]
                  if (file) handleFileUpload(file)
                }
                input.click()
              }}
              role="button"
              tabIndex={0}
            >
              <FileText className="text-muted-foreground size-5" aria-hidden />
              <p className="mt-3 font-medium">Drop a .txt file here, or click to choose</p>
              <p className="text-muted-foreground mt-1">ip:port, one per line</p>
            </div>
          ) : (
            <div className="space-y-4">
              <div className="flex items-baseline justify-between gap-4">
                <div className="min-w-0">
                  <p className="truncate font-medium">{importFile.name}</p>
                  <p className="text-muted-foreground">
                    <span className="num text-foreground font-medium">{count(parsedProxies.length)}</span> valid lines
                  </p>
                </div>
                {!isImporting && !importDone && (
                  <button type="button" className="text-muted-foreground hover:text-foreground font-medium" onClick={() => { setImportFile(null); setParsedProxies([]) }}>
                    Change file
                  </button>
                )}
              </div>

              {parsedProxies.length > 0 && (
                <>
                  <div>
                    <p className="label mb-1.5">Preview</p>
                    <div className="border-border max-h-28 overflow-y-auto rounded-md border px-2.5 py-2 font-mono text-[0.75rem] leading-5">
                      {parsedProxies.slice(0, 10).map((p, i) => (
                        <div key={i} className="text-muted-foreground">{p}</div>
                      ))}
                      {parsedProxies.length > 10 && <div className="text-muted-foreground">… and {count(parsedProxies.length - 10)} more</div>}
                    </div>
                  </div>

                  <div className="space-y-1.5">
                    <Label htmlFor="import-protocol">Protocol for every line</Label>
                    <ProtocolSelect id="import-protocol" value={importProtocol} onChange={setImportProtocol} disabled={isImporting} />
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-1.5">
                      <Label htmlFor="import-username">Username (optional)</Label>
                      <Input id="import-username" value={importUsername} onChange={(e) => setImportUsername(e.target.value)} disabled={isImporting} />
                    </div>
                    <div className="space-y-1.5">
                      <Label htmlFor="import-password">Password (optional)</Label>
                      <Input id="import-password" type="password" value={importPassword} onChange={(e) => setImportPassword(e.target.value)} disabled={isImporting} />
                    </div>
                  </div>

                  {(isImporting || importDone) && (
                    <div className="space-y-2">
                      <div className="flex items-baseline justify-between">
                        <span className="num">
                          {importProgress.current} / {importProgress.total}
                        </span>
                        <span className="text-muted-foreground num">
                          {importProgress.success} added
                          {importProgress.skipped > 0 && ` · ${importProgress.skipped} skipped`}
                          {importProgress.failed > 0 && ` · ${importProgress.failed} failed`}
                        </span>
                      </div>
                      <UsageBar value={(importProgress.current / Math.max(1, importProgress.total)) * 100} />
                      {importDone && importResults.some((r) => r.status !== "success") && (
                        <div className="border-border max-h-40 overflow-y-auto rounded-md border px-2.5 py-2 font-mono text-[0.75rem] leading-5">
                          {importResults
                            .filter((r) => r.status !== "success")
                            .map((r, i) => (
                              <div key={i} className="text-muted-foreground">
                                {r.address} <span className="ml-2">{r.error}</span>
                              </div>
                            ))}
                        </div>
                      )}
                    </div>
                  )}
                </>
              )}
            </div>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => { setImportOpen(false); resetImport() }} disabled={isImporting}>
              {importDone ? "Close" : "Cancel"}
            </Button>
            {importFile && parsedProxies.length > 0 && !importDone && (
              <Button onClick={handleImport} disabled={isImporting}>
                {isImporting ? "Importing…" : `Import ${count(parsedProxies.length)} proxies`}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmations */}
      <AlertDialog open={deleteId !== null} onOpenChange={(open) => !open && setDeleteId(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this proxy?</AlertDialogTitle>
            <AlertDialogDescription>It leaves every pool it belongs to and its request history is dropped. This cannot be undone.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={confirmDelete}>Delete</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={bulkDeleteOpen} onOpenChange={setBulkDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {selectedIds.length} proxies?</AlertDialogTitle>
            <AlertDialogDescription>They leave their pools and their request history is dropped. This cannot be undone.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={confirmBulkDelete}>Delete</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={deleteAllOpen} onOpenChange={setDeleteAllOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete every proxy?</AlertDialogTitle>
            <AlertDialogDescription>
              All {count(total)} proxies in the database are removed, including pool members. Sources will re-import on their next fetch. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={confirmDeleteAll}>Delete all</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

export default function Page() {
  return (
    <Suspense fallback={<LoadingLine />}>
      <ProxiesPage />
    </Suspense>
  )
}
