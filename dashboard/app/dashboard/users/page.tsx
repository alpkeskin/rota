"use client"

import { useEffect, useRef, useState, useCallback } from "react"
import { Check, ChevronDown, Copy, Eye, EyeOff } from "lucide-react"
import { toast } from "@/lib/toast"
import { api } from "@/lib/api"
import { PROXY_PORT } from "@/lib/config"
import { ProxyUser, ProxyPool, CreateProxyUserRequest, UpdateProxyUserRequest } from "@/lib/types"
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
import { PageHeader, Content, Section, EmptyLine, LoadingLine } from "@/components/page-header"
import { StatStrip } from "@/components/stat-strip"
import { Tag } from "@/components/status"
import { bytes, count } from "@/lib/format"

const GiB = 1024 ** 3

const DEFAULT_FORM: CreateProxyUserRequest = {
  username: "",
  password: "",
  enabled: true,
  allow_working_proxies_export: false,
  main_pool_id: null,
  fallback_pool_ids: [],
  max_retries: 5,
  requests_per_minute: 0,
  monthly_bandwidth_limit_bytes: 0,
  max_concurrent_connections: 0,
}

export default function UsersPage() {
  const [users, setUsers] = useState<ProxyUser[]>([])
  const [pools, setPools] = useState<ProxyPool[]>([])
  const [loading, setLoading] = useState(true)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editUser, setEditUser] = useState<ProxyUser | null>(null)
  const [form, setForm] = useState<CreateProxyUserRequest>(DEFAULT_FORM)
  const [saving, setSaving] = useState(false)
  const [showPass, setShowPass] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ProxyUser | null>(null)

  // Export-link dialog
  const [apiLinkUser, setApiLinkUser] = useState<ProxyUser | null>(null)
  const [apiLinkPool, setApiLinkPool] = useState<string>("default")
  const [apiLinkCount, setApiLinkCount] = useState<string>("")
  const [apiLinkFormat, setApiLinkFormat] = useState<string>("raw")
  const [copiedLink, setCopiedLink] = useState<string | null>(null)
  const copiedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Plaintext token, held only while the dialog that generated it is open.
  const [apiLinkToken, setApiLinkToken] = useState<string | null>(null)
  const [tokenBusy, setTokenBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      const [usersRes, poolsRes] = await Promise.all([api.getProxyUsers(), api.getPools()])
      setUsers(usersRes.users)
      setPools(poolsRes.pools)
    } catch {
      toast.error("Failed to load users")
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const poolName = (id?: number | null) => {
    if (!id) return "—"
    return pools.find((p) => p.id === id)?.name ?? `Pool #${id}`
  }

  const openCreate = () => {
    setEditUser(null)
    setForm(DEFAULT_FORM)
    setShowPass(false)
    setDialogOpen(true)
  }

  const openEdit = (u: ProxyUser) => {
    setEditUser(u)
    setForm({
      username: u.username,
      password: "",
      enabled: u.enabled,
      allow_working_proxies_export: u.allow_working_proxies_export ?? false,
      main_pool_id: u.main_pool_id ?? null,
      fallback_pool_ids: u.fallback_pool_ids ?? [],
      max_retries: u.max_retries,
      requests_per_minute: u.requests_per_minute ?? 0,
      monthly_bandwidth_limit_bytes: u.monthly_bandwidth_limit_bytes ?? 0,
      max_concurrent_connections: u.max_concurrent_connections ?? 0,
    })
    setShowPass(false)
    setDialogOpen(true)
  }

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.username.trim()) {
      toast.error("Username is required")
      return
    }
    if (!editUser && !form.password) {
      toast.error("Password is required")
      return
    }
    setSaving(true)
    try {
      if (editUser) {
        const upd: UpdateProxyUserRequest = {
          enabled: form.enabled,
          allow_working_proxies_export: form.allow_working_proxies_export,
          main_pool_id: form.main_pool_id,
          fallback_pool_ids: form.fallback_pool_ids,
          max_retries: form.max_retries,
          requests_per_minute: form.requests_per_minute,
          monthly_bandwidth_limit_bytes: form.monthly_bandwidth_limit_bytes,
          max_concurrent_connections: form.max_concurrent_connections,
        }
        if (form.password) upd.password = form.password
        await api.updateProxyUser(editUser.id, upd)
        toast.success("User updated")
      } else {
        await api.createProxyUser(form)
        toast.success("User created")
      }
      setDialogOpen(false)
      load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to save user")
    } finally {
      setSaving(false)
    }
  }

  const confirmDelete = async () => {
    if (!deleteTarget) return
    try {
      await api.deleteProxyUser(deleteTarget.id)
      toast.success("User deleted")
      load()
    } catch {
      toast.error("Failed to delete user")
    } finally {
      setDeleteTarget(null)
    }
  }

  const toggleEnabled = async (u: ProxyUser) => {
    try {
      await api.updateProxyUser(u.id, { enabled: !u.enabled })
      load()
    } catch {
      toast.error("Failed to update user")
    }
  }

  const toggleAllowExport = async (u: ProxyUser) => {
    try {
      await api.updateProxyUser(u.id, { allow_working_proxies_export: !u.allow_working_proxies_export })
      load()
    } catch {
      toast.error("Failed to update export permission")
    }
  }

  const toggleFallback = (poolId: number) => {
    const current = form.fallback_pool_ids ?? []
    setForm({
      ...form,
      fallback_pool_ids: current.includes(poolId) ? current.filter((x) => x !== poolId) : [...current, poolId],
    })
  }

  const copyProxyURL = (u: ProxyUser) => {
    const host = window.location.hostname
    navigator.clipboard.writeText(`http://${u.username}:***@${host}:${PROXY_PORT}`)
    toast.success("Proxy URL copied", "Replace *** with the user's password")
  }

  const openApiLinkModal = (u: ProxyUser) => {
    setApiLinkUser(u)
    setApiLinkPool("default")
    setApiLinkCount("")
    setApiLinkFormat("raw")
    setCopiedLink(null)
    setApiLinkToken(null)
  }

  const closeApiLinkModal = () => {
    setApiLinkUser(null)
    setApiLinkToken(null)
  }

  const applyTokenState = (id: number, has: boolean, createdAt?: string) => {
    const patch = (u: ProxyUser) => (u.id === id ? { ...u, has_export_token: has, export_token_created_at: createdAt } : u)
    setUsers((prev) => prev.map(patch))
    setApiLinkUser((prev) => (prev ? patch(prev) : prev))
  }

  const rotateExportToken = async (u: ProxyUser) => {
    setTokenBusy(true)
    try {
      const res = await api.rotateProxyUserExportToken(u.id)
      setApiLinkToken(res.token)
      applyTokenState(u.id, true, res.created_at)
      toast.success(u.has_export_token ? "Token regenerated — the old one no longer works" : "Export token generated")
    } catch {
      toast.error("Failed to generate export token")
    } finally {
      setTokenBusy(false)
    }
  }

  const revokeExportToken = async (u: ProxyUser) => {
    setTokenBusy(true)
    try {
      await api.revokeProxyUserExportToken(u.id)
      setApiLinkToken(null)
      applyTokenState(u.id, false)
      toast.success("Export token revoked")
    } catch {
      toast.error("Failed to revoke export token")
    } finally {
      setTokenBusy(false)
    }
  }

  // Export only serves the user's own pools: its main pool and fallbacks.
  const exportablePools = (u: ProxyUser) =>
    pools.filter((p) => p.id !== u.main_pool_id && (u.fallback_pool_ids ?? []).includes(p.id))

  // withToken: put the token in the query string, for tools that only take a
  // URL. Otherwise the token goes in the Authorization header (see exportCurl).
  const generateExportUrl = (withToken: boolean) => {
    const protocol = window.location.protocol
    const host = window.location.hostname
    const port = process.env.NEXT_PUBLIC_API_PORT || (window.location.port ? window.location.port : protocol === "https:" ? "443" : "80")
    const portSuffix = port === "80" || port === "443" ? "" : `:${port}`
    const baseUrl = `${protocol}//${host}${portSuffix}/api/v1/proxy-users/export-working-proxies`
    const params = new URLSearchParams()
    if (withToken) params.set("token", apiLinkToken ?? "YOUR_EXPORT_TOKEN")
    if (apiLinkPool && apiLinkPool !== "default") params.set("pool", apiLinkPool)
    if (apiLinkCount && parseInt(apiLinkCount) > 0) params.set("count", apiLinkCount)
    if (apiLinkFormat === "url") params.set("format", "url")
    const query = params.toString()
    return query ? `${baseUrl}?${query}` : baseUrl
  }

  const exportCurl = () =>
    `curl -H "Authorization: Bearer ${apiLinkToken ?? "YOUR_EXPORT_TOKEN"}" "${generateExportUrl(false)}"`

  const enabled = users.filter((u) => u.enabled).length
  const withPool = users.filter((u) => u.main_pool_id).length

  return (
    <>
      <PageHeader
        title="Users"
        description={
          <>
            Credentials clients use on port <span className="font-mono">{PROXY_PORT}</span>. A user is routed through its main pool, then its fallbacks in order; without a pool it rotates over the whole inventory. Append <span className="font-mono">-country-us</span>, <span className="font-mono">-city-new_york</span> or <span className="font-mono">-session-&lt;id&gt;</span> to the username to pick the exit country, city or a sticky IP.
          </>
        }
      >
        <Button onClick={openCreate}>Add user</Button>
      </PageHeader>

      <StatStrip
        columns={4}
        stats={[
          { label: "Users", value: count(users.length), hint: `${count(enabled)} enabled` },
          { label: "With a main pool", value: count(withPool), hint: `${count(users.length - withPool)} on global rotation` },
          { label: "Pools available", value: count(pools.length), hint: `${count(pools.filter((p) => p.enabled).length)} enabled` },
          { label: "Export allowed", value: count(users.filter((u) => u.allow_working_proxies_export).length), hint: "can pull working lists via API" },
        ]}
      />

      <Content>
        {loading ? (
          <LoadingLine />
        ) : users.length === 0 ? (
          <EmptyLine>No users yet. Clients cannot authenticate to the proxy until one exists.</EmptyLine>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Username</TableHead>
                <TableHead>Main pool</TableHead>
                <TableHead>Fallbacks</TableHead>
                <TableHead className="text-right">Max retries</TableHead>
                <TableHead className="text-right">Rate limit</TableHead>
                <TableHead className="text-right">This month</TableHead>
                <TableHead>Enabled</TableHead>
                <TableHead>Export API</TableHead>
                <TableHead className="w-8" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.map((u) => (
                <TableRow key={u.id}>
                  <TableCell className="font-mono font-medium">{u.username}</TableCell>
                  <TableCell>
                    {u.main_pool_id ? (
                      <Tag strong>{u.main_pool_name || poolName(u.main_pool_id)}</Tag>
                    ) : (
                      <span className="text-muted-foreground">global rotation</span>
                    )}
                  </TableCell>
                  <TableCell>
                    {u.fallback_pool_ids && u.fallback_pool_ids.length > 0 ? (
                      <span className="flex flex-wrap gap-1">
                        {u.fallback_pool_ids.map((id, i) => (
                          <Tag key={id}>
                            {i + 1}. {poolName(id)}
                          </Tag>
                        ))}
                      </span>
                    ) : (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  <TableCell className="num text-right">{u.max_retries}</TableCell>
                  <TableCell className="num text-muted-foreground text-right">
                    {u.requests_per_minute > 0 ? `${count(u.requests_per_minute)}/min` : "unlimited"}
                  </TableCell>
                  <TableCell
                    className={
                      "num text-right " +
                      (u.monthly_bandwidth_limit_bytes > 0 && (u.bandwidth_used_bytes ?? 0) >= u.monthly_bandwidth_limit_bytes ? "text-critical" : "text-muted-foreground")
                    }
                    title={u.max_concurrent_connections > 0 ? `Up to ${count(u.max_concurrent_connections)} connections at once` : undefined}
                  >
                    {bytes(u.bandwidth_used_bytes ?? 0)}
                    {u.monthly_bandwidth_limit_bytes > 0 && <> / {bytes(u.monthly_bandwidth_limit_bytes)}</>}
                  </TableCell>
                  <TableCell>
                    <Switch checked={u.enabled} onCheckedChange={() => toggleEnabled(u)} aria-label={`${u.username} enabled`} />
                  </TableCell>
                  <TableCell>
                    <Switch
                      checked={u.allow_working_proxies_export ?? false}
                      onCheckedChange={() => toggleAllowExport(u)}
                      aria-label={`${u.username} export allowed`}
                    />
                  </TableCell>
                  <TableCell className="text-right">
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${u.username}`}>
                          <ChevronDown aria-hidden />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => copyProxyURL(u)}>Copy proxy URL</DropdownMenuItem>
                        {u.allow_working_proxies_export && (
                          <DropdownMenuItem onClick={() => openApiLinkModal(u)}>Export link…</DropdownMenuItem>
                        )}
                        <DropdownMenuItem onClick={() => openEdit(u)}>Edit</DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem variant="destructive" onClick={() => setDeleteTarget(u)}>Delete</DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Content>

      <Section title="How routing works" className="border-b-0">
        <dl className="grid gap-x-8 gap-y-4 sm:grid-cols-3">
          <div>
            <dt className="label">Authentication</dt>
            <dd className="mt-0.5">
              Clients send Proxy-Authorization: <span className="font-mono">http://user:pass@host:{PROXY_PORT}</span>.
            </dd>
          </div>
          <div>
            <dt className="label">Pool chain</dt>
            <dd className="mt-0.5">Main pool first; when it has no alive proxy, requests cascade to fallbacks in order. Each pool keeps its own rotation.</dd>
          </div>
          <div>
            <dt className="label">Retries</dt>
            <dd className="mt-0.5">Each retry picks a fresh proxy across the chain. Proxies that failed are skipped until the next refresh.</dd>
          </div>
        </dl>
      </Section>

      {/* Add / edit */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-h-[90vh] overflow-y-auto">
          <form onSubmit={handleSave} className="space-y-4">
            <DialogHeader>
              <DialogTitle>{editUser ? `Edit ${editUser.username}` : "Add user"}</DialogTitle>
              <DialogDescription>
                {editUser ? "Open connections keep their current proxy; new requests use the updated chain." : "The user can authenticate as soon as it is saved."}
              </DialogDescription>
            </DialogHeader>

            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="user-name">Username</Label>
                <Input id="user-name" className="font-mono" required value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} disabled={!!editUser} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="user-pass">{editUser ? "New password" : "Password"}</Label>
                <div className="relative">
                  <Input
                    id="user-pass"
                    type={showPass ? "text" : "password"}
                    placeholder={editUser ? "leave blank to keep" : "min 6 characters"}
                    value={form.password}
                    onChange={(e) => setForm({ ...form, password: e.target.value })}
                    className="pr-8"
                  />
                  <button
                    type="button"
                    className="text-muted-foreground hover:text-foreground absolute top-1/2 right-2 -translate-y-1/2"
                    onClick={() => setShowPass((v) => !v)}
                    aria-label={showPass ? "Hide password" : "Show password"}
                  >
                    {showPass ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                  </button>
                </div>
              </div>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="user-pool">Main pool</Label>
              <Select value={form.main_pool_id?.toString() ?? "none"} onValueChange={(v) => setForm({ ...form, main_pool_id: v === "none" ? null : parseInt(v) })}>
                <SelectTrigger id="user-pool" className="w-full">
                  <SelectValue placeholder="Select main pool" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">No pool — global rotation</SelectItem>
                  {pools.map((p) => (
                    <SelectItem key={p.id} value={p.id.toString()}>
                      {p.name} <span className="text-muted-foreground">({p.active_proxies}/{p.total_proxies} active)</span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="space-y-1.5">
              <Label>Fallback pools, in priority order</Label>
              {pools.length === 0 ? (
                <p className="text-muted-foreground">No pools yet.</p>
              ) : (
                <div className="border-border max-h-44 divide-y overflow-y-auto rounded-md border">
                  {pools
                    .filter((p) => p.id !== form.main_pool_id)
                    .map((p) => {
                      const checked = (form.fallback_pool_ids ?? []).includes(p.id)
                      const idx = (form.fallback_pool_ids ?? []).indexOf(p.id)
                      return (
                        <label key={p.id} className="hover:bg-accent/50 flex cursor-pointer items-center gap-3 px-2.5 py-1.5">
                          <Checkbox checked={checked} onCheckedChange={() => toggleFallback(p.id)} />
                          <span className="min-w-0 flex-1 truncate font-medium">{p.name}</span>
                          <span className="text-muted-foreground num">
                            {p.active_proxies} active · {p.rotation_method}
                          </span>
                          {checked && <Tag strong>#{idx + 1}</Tag>}
                        </label>
                      )
                    })}
                </div>
              )}
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="user-retries">Max retries across the chain</Label>
                <Input id="user-retries" type="number" min={1} max={50} value={form.max_retries} onChange={(e) => setForm({ ...form, max_retries: parseInt(e.target.value) || 5 })} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="user-rpm">Rate limit, requests/min</Label>
                <Input id="user-rpm" type="number" min={0} value={form.requests_per_minute ?? 0} onChange={(e) => setForm({ ...form, requests_per_minute: parseInt(e.target.value) || 0 })} />
                <p className="text-muted-foreground text-[0.6875rem] leading-4">0 = unlimited. Over the limit the proxy answers 429.</p>
              </div>
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="user-quota">Monthly bandwidth, GB</Label>
                <Input
                  id="user-quota"
                  type="number"
                  min={0}
                  step="any"
                  value={form.monthly_bandwidth_limit_bytes ? +(form.monthly_bandwidth_limit_bytes / GiB).toFixed(3) : 0}
                  onChange={(e) => setForm({ ...form, monthly_bandwidth_limit_bytes: Math.max(0, Math.round((parseFloat(e.target.value) || 0) * GiB)) })}
                />
                <p className="text-muted-foreground text-[0.6875rem] leading-4">Up + down, per calendar month (UTC). 0 = unlimited. Open tunnels close when it runs out.</p>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="user-conns">Max concurrent connections</Label>
                <Input id="user-conns" type="number" min={0} value={form.max_concurrent_connections ?? 0} onChange={(e) => setForm({ ...form, max_concurrent_connections: Math.max(0, parseInt(e.target.value) || 0) })} />
                <p className="text-muted-foreground text-[0.6875rem] leading-4">Open requests and tunnels at once. 0 = unlimited.</p>
              </div>
            </div>

            <div className="flex items-start justify-between gap-4">
              <div>
                <Label htmlFor="user-export" className="text-foreground">Allow working-proxy export</Label>
                <p className="text-muted-foreground mt-1 text-[0.6875rem] leading-4">Lets this user fetch its pool&apos;s alive proxies as a list over the API.</p>
              </div>
              <Switch id="user-export" checked={form.allow_working_proxies_export ?? false} onCheckedChange={(v) => setForm({ ...form, allow_working_proxies_export: v })} />
            </div>
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="user-enabled" className="text-foreground">Enabled</Label>
              <Switch id="user-enabled" checked={form.enabled} onCheckedChange={(v) => setForm({ ...form, enabled: v })} />
            </div>

            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setDialogOpen(false)}>Cancel</Button>
              <Button type="submit" disabled={saving}>{saving ? "Saving…" : editUser ? "Save changes" : "Create user"}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Export link */}
      <Dialog open={!!apiLinkUser} onOpenChange={(open) => !open && closeApiLinkModal()}>
        <DialogContent className="sm:max-w-[32rem]">
          <DialogHeader>
            <DialogTitle>Export link for {apiLinkUser?.username}</DialogTitle>
            <DialogDescription>A GET that returns the user&apos;s alive proxies, one per line. It authenticates with a revocable export token instead of the user&apos;s proxy password.</DialogDescription>
          </DialogHeader>
          {apiLinkUser && (
            <div className="space-y-4">
              <div className="space-y-1.5">
                <Label>Export token</Label>
                {apiLinkToken ? (
                  <p className="text-muted-foreground text-[0.6875rem] leading-4">
                    Copy the link now — the token is shown only once. Regenerating it invalidates this one.
                  </p>
                ) : apiLinkUser.has_export_token ? (
                  <p className="text-muted-foreground text-[0.6875rem] leading-4">
                    A token was issued{apiLinkUser.export_token_created_at ? ` on ${new Date(apiLinkUser.export_token_created_at).toLocaleString()}` : ""}. It can&apos;t be shown again; regenerate to get a new link.
                  </p>
                ) : (
                  <p className="text-muted-foreground text-[0.6875rem] leading-4">No token yet. Generate one to build a working link.</p>
                )}
                <div className="flex gap-2">
                  <Button type="button" variant="outline" size="sm" disabled={tokenBusy} onClick={() => rotateExportToken(apiLinkUser)}>
                    {apiLinkUser.has_export_token ? "Regenerate token" : "Generate token"}
                  </Button>
                  {apiLinkUser.has_export_token && (
                    <Button type="button" variant="outline" size="sm" disabled={tokenBusy} onClick={() => revokeExportToken(apiLinkUser)}>
                      Revoke
                    </Button>
                  )}
                </div>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="link-pool">Pool</Label>
                <Select value={apiLinkPool} onValueChange={setApiLinkPool}>
                  <SelectTrigger id="link-pool" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="default">User&apos;s main pool ({apiLinkUser.main_pool_id ? poolName(apiLinkUser.main_pool_id) : "none"})</SelectItem>
                    {exportablePools(apiLinkUser).map((p) => (
                      <SelectItem key={p.id} value={p.name}>
                        {p.name} <span className="text-muted-foreground">({p.active_proxies} active)</span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label htmlFor="link-count">Limit (optional)</Label>
                  <Input id="link-count" type="number" min={1} placeholder="all" value={apiLinkCount} onChange={(e) => setApiLinkCount(e.target.value)} />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="link-format">Line format</Label>
                  <Select value={apiLinkFormat} onValueChange={setApiLinkFormat}>
                    <SelectTrigger id="link-format" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="raw">address[:user:pass]</SelectItem>
                      <SelectItem value="url">protocol://[user:pass@]address</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>
              {[
                { id: "link-curl", label: "curl (token in header — preferred)", value: exportCurl() },
                { id: "link-url", label: "URL (token in query — for tools that only take a URL)", value: generateExportUrl(true) },
              ].map((field) => (
                <div key={field.id} className="space-y-1.5">
                  <Label htmlFor={field.id}>{field.label}</Label>
                  <div className="flex items-center gap-2">
                    <Input id={field.id} readOnly value={field.value} className="font-mono text-[0.75rem] select-all" />
                    <Button
                      type="button"
                      variant="outline"
                      className="shrink-0"
                      onClick={() => {
                        navigator.clipboard.writeText(field.value)
                        setCopiedLink(field.id)
                        if (copiedTimer.current) clearTimeout(copiedTimer.current)
                        copiedTimer.current = setTimeout(() => setCopiedLink(null), 2000)
                      }}
                    >
                      {copiedLink === field.id ? <Check aria-hidden /> : <Copy aria-hidden />}
                      {copiedLink === field.id ? "Copied" : "Copy"}
                    </Button>
                  </div>
                </div>
              ))}
              <p className="text-muted-foreground text-[0.6875rem] leading-4">
                {apiLinkToken ? (
                  <>A token in a URL can end up in access logs and shell history; prefer the header form, and revoke the token if a link leaks.</>
                ) : (
                  <>Replace <span className="font-mono">YOUR_EXPORT_TOKEN</span> with the user&apos;s export token before use.</>
                )}
              </p>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={closeApiLinkModal}>Close</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete user “{deleteTarget?.username}”?</AlertDialogTitle>
            <AlertDialogDescription>Clients using these credentials get 407 on their next request. Pools are not affected.</AlertDialogDescription>
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
