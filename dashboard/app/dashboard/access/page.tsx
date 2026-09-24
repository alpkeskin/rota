"use client"

import * as React from "react"
import { Suspense } from "react"
import { AlertTriangle, Check, CheckCircle2, ChevronDown, CircleMinus, Copy, XCircle, type LucideIcon } from "lucide-react"
import { toast } from "@/lib/toast"
import { api } from "@/lib/api"
import { roleAtLeast, useCan, useSession } from "@/lib/session"
import type { Account, ApiKey, AuditEntry, Role } from "@/lib/types"
import { useUrlState } from "@/hooks/use-url-state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
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
import { PageHeader, Content, TabStrip, EmptyLine, LoadingLine } from "@/components/page-header"
import { SearchInput } from "@/components/controls"
import { Pagination } from "@/components/pagination"
import { StatusLabel, Tag } from "@/components/status"
import { formatDateTime, relative } from "@/lib/format"

const ROLES: { value: Role; label: string; hint: string }[] = [
  { value: "viewer", label: "Viewer", hint: "Read everything except accounts, the audit log and secrets" },
  { value: "operator", label: "Operator", hint: "Also change proxies, sources, pools and proxy users" },
  { value: "admin", label: "Admin", hint: "Also change settings, manage accounts and read the audit log" },
]

const errMsg = (e: unknown, fallback: string) => (e instanceof Error && e.message ? e.message : fallback)

function RoleSelect({ id, value, onChange, max, disabled }: { id?: string; value: Role; onChange: (r: Role) => void; max?: Role; disabled?: boolean }) {
  return (
    <Select value={value} onValueChange={(v) => onChange(v as Role)} disabled={disabled}>
      <SelectTrigger id={id} className="w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {ROLES.filter((r) => !max || roleAtLeast(max, r.value)).map((r) => (
          <SelectItem key={r.value} value={r.value}>
            {r.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

function CopyField({ id, value }: { id: string; value: string }) {
  const [copied, setCopied] = React.useState(false)
  const timer = React.useRef<ReturnType<typeof setTimeout> | null>(null)
  React.useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current)
  }, [])
  return (
    <div className="flex items-center gap-2">
      <Input id={id} readOnly value={value} className="font-mono text-[0.75rem] select-all" />
      <Button
        type="button"
        variant="outline"
        className="shrink-0"
        onClick={() => {
          navigator.clipboard.writeText(value)
          setCopied(true)
          if (timer.current) clearTimeout(timer.current)
          timer.current = setTimeout(() => setCopied(false), 2000)
        }}
      >
        {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
        {copied ? "Copied" : "Copy"}
      </Button>
    </div>
  )
}

// ── API keys ────────────────────────────────────────────────────────────────

function keyState(k: ApiKey): { label: string; tone: "good" | "muted" } {
  if (k.revoked_at) return { label: "Revoked", tone: "muted" }
  if (k.expires_at && new Date(k.expires_at) <= new Date()) return { label: "Expired", tone: "muted" }
  return { label: "Active", tone: "good" }
}

function ApiKeysTab() {
  const me = useSession()
  const isAdmin = useCan("admin")
  const [keys, setKeys] = React.useState<ApiKey[]>([])
  const [loading, setLoading] = React.useState(true)
  const [showAll, setShowAll] = React.useState(false)
  const [createOpen, setCreateOpen] = React.useState(false)
  const [name, setName] = React.useState("")
  const [password, setPassword] = React.useState("")
  const [role, setRole] = React.useState<Role>(me.role)
  const [expiry, setExpiry] = React.useState("90")
  const [saving, setSaving] = React.useState(false)
  const [created, setCreated] = React.useState<string | null>(null)
  const [revokeTarget, setRevokeTarget] = React.useState<ApiKey | null>(null)

  const load = React.useCallback(async () => {
    try {
      setKeys((await api.getApiKeys(showAll)).api_keys)
    } catch (e) {
      toast.error(errMsg(e, "Failed to load API keys"))
    } finally {
      setLoading(false)
    }
  }, [showAll])

  React.useEffect(() => {
    load()
  }, [load])

  const openCreate = () => {
    setName("")
    setPassword("")
    setRole(me.role)
    setExpiry("90")
    setCreated(null)
    setCreateOpen(true)
  }

  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    setSaving(true)
    try {
      const res = await api.createApiKey({ current_password: password, name, role, expires_in_days: parseInt(expiry) || 0 })
      setPassword("")
      setCreated(res.key)
      load()
    } catch (err) {
      toast.error(errMsg(err, "Failed to create API key"))
    } finally {
      setSaving(false)
    }
  }

  const revoke = async () => {
    if (!revokeTarget) return
    try {
      await api.revokeApiKey(revokeTarget.id)
      toast.success(`Revoked “${revokeTarget.name}”`)
      load()
    } catch (e) {
      toast.error(errMsg(e, "Failed to revoke API key"))
    } finally {
      setRevokeTarget(null)
    }
  }

  return (
    <Content>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <p className="text-muted-foreground max-w-2xl">
          Keys authenticate scripts and integrations with <span className="font-mono">Authorization: Bearer rota_key_…</span>. A key acts with its own role, capped by its owner&apos;s current role, and can&apos;t manage accounts or other keys.
        </p>
        <div className="flex items-center gap-3">
          {isAdmin && (
            <label className="text-muted-foreground flex items-center gap-2">
              <Switch checked={showAll} onCheckedChange={setShowAll} aria-label="Show every account's keys" />
              All accounts
            </label>
          )}
          <Button onClick={openCreate} disabled={me.via !== "session"}>
            Create key
          </Button>
        </div>
      </div>

      {loading ? (
        <LoadingLine />
      ) : keys.length === 0 ? (
        <EmptyLine>No API keys yet.</EmptyLine>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Key</TableHead>
              {showAll && <TableHead>Owner</TableHead>}
              <TableHead>Role</TableHead>
              <TableHead>Last used</TableHead>
              <TableHead>Expires</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {keys.map((k) => {
              const st = keyState(k)
              return (
                <TableRow key={k.id}>
                  <TableCell className="font-medium">{k.name}</TableCell>
                  <TableCell className="font-mono">{k.prefix}…</TableCell>
                  {showAll && <TableCell>{k.username}</TableCell>}
                  <TableCell>
                    <Tag className="capitalize">{k.role}</Tag>
                  </TableCell>
                  <TableCell className="text-muted-foreground">{k.last_used_at ? relative(k.last_used_at) : "Never"}</TableCell>
                  <TableCell className="text-muted-foreground">{k.expires_at ? formatDateTime(k.expires_at) : "Never"}</TableCell>
                  <TableCell>
                    <StatusLabel icon={st.tone === "good" ? CheckCircle2 : CircleMinus} tone={st.tone}>
                      {st.label}
                    </StatusLabel>
                  </TableCell>
                  <TableCell className="text-right">
                    {st.label === "Active" && (
                      <Button variant="ghost" size="sm" onClick={() => setRevokeTarget(k)}>
                        Revoke
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}

      <Dialog open={createOpen} onOpenChange={(o) => !o && setCreateOpen(false)}>
        <DialogContent className="sm:max-w-[30rem]">
          <DialogHeader>
            <DialogTitle>{created ? "Copy your API key" : "Create API key"}</DialogTitle>
            <DialogDescription>
              {created ? "This is the only time the key is shown. Store it somewhere safe; if it leaks, revoke it." : "Give the key a name that says where it's used."}
            </DialogDescription>
          </DialogHeader>
          {created ? (
            <CopyField id="new-key" value={created} />
          ) : (
            <form id="create-key" onSubmit={create} className="space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="key-name">Name</Label>
                <Input id="key-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. ci-deploy" autoFocus />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label htmlFor="key-role">Role</Label>
                  <RoleSelect id="key-role" value={role} onChange={setRole} max={me.role} />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="key-expiry">Expires</Label>
                  <Select value={expiry} onValueChange={setExpiry}>
                    <SelectTrigger id="key-expiry" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="30">In 30 days</SelectItem>
                      <SelectItem value="90">In 90 days</SelectItem>
                      <SelectItem value="365">In a year</SelectItem>
                      <SelectItem value="0">Never</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>
              <p className="text-muted-foreground text-[0.6875rem] leading-4">{ROLES.find((r) => r.value === role)?.hint}.</p>
              <div className="space-y-1.5">
                <Label htmlFor="key-password">Your password</Label>
                <Input id="key-password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
                <p className="text-muted-foreground text-[0.6875rem] leading-4">Confirms it&apos;s you: a key outlives sign-outs, so a stolen session alone can&apos;t create one.</p>
              </div>
            </form>
          )}
          <DialogFooter>
            {created ? (
              <Button onClick={() => setCreateOpen(false)}>Done</Button>
            ) : (
              <>
                <Button variant="outline" onClick={() => setCreateOpen(false)}>
                  Cancel
                </Button>
                <Button type="submit" form="create-key" disabled={saving || !name.trim() || !password}>
                  {saving ? "Creating…" : "Create key"}
                </Button>
              </>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={!!revokeTarget} onOpenChange={(o) => !o && setRevokeTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke “{revokeTarget?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>Anything using this key gets 401 on its next request. This can&apos;t be undone.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={revoke}>Revoke</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Content>
  )
}

// ── Accounts (admin) ────────────────────────────────────────────────────────

function AccountsTab() {
  const me = useSession()
  const [accounts, setAccounts] = React.useState<Account[]>([])
  const [loading, setLoading] = React.useState(true)
  const [createOpen, setCreateOpen] = React.useState(false)
  const [form, setForm] = React.useState<{ username: string; password: string; role: Role }>({ username: "", password: "", role: "viewer" })
  const [saving, setSaving] = React.useState(false)
  const [resetTarget, setResetTarget] = React.useState<Account | null>(null)
  const [resetPass, setResetPass] = React.useState("")
  const [deleteTarget, setDeleteTarget] = React.useState<Account | null>(null)

  const load = React.useCallback(async () => {
    try {
      setAccounts((await api.getAccounts()).accounts)
    } catch (e) {
      toast.error(errMsg(e, "Failed to load accounts"))
    } finally {
      setLoading(false)
    }
  }, [])

  React.useEffect(() => {
    load()
  }, [load])

  const update = async (a: Account, patch: { role?: Role; enabled?: boolean }, done: string) => {
    try {
      await api.updateAccount(a.id, patch)
      toast.success(done)
      load()
    } catch (e) {
      toast.error(errMsg(e, "Failed to update account"))
    }
  }

  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    if (form.password.length < 8) return toast.error("Password must be at least 8 characters")
    setSaving(true)
    try {
      await api.createAccount(form)
      toast.success(`Created ${form.username}`)
      setCreateOpen(false)
      load()
    } catch (err) {
      toast.error(errMsg(err, "Failed to create account"))
    } finally {
      setSaving(false)
    }
  }

  const resetPassword = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!resetTarget) return
    if (resetPass.length < 8) return toast.error("Password must be at least 8 characters")
    try {
      await api.updateAccount(resetTarget.id, { password: resetPass })
      toast.success(`Password reset for ${resetTarget.username}`, "Their sessions were signed out")
      setResetTarget(null)
    } catch (err) {
      toast.error(errMsg(err, "Failed to reset password"))
    }
  }

  const revokeSessions = async (a: Account) => {
    try {
      await api.revokeAccountSessions(a.id)
      toast.success(`Signed ${a.username} out everywhere`)
    } catch (e) {
      toast.error(errMsg(e, "Failed to sign out"))
    }
  }

  const remove = async () => {
    if (!deleteTarget) return
    try {
      await api.deleteAccount(deleteTarget.id)
      toast.success(`Deleted ${deleteTarget.username}`)
      load()
    } catch (e) {
      toast.error(errMsg(e, "Failed to delete account"))
    } finally {
      setDeleteTarget(null)
    }
  }

  return (
    <Content>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <p className="text-muted-foreground max-w-2xl">
          Dashboard and API sign-ins. Role changes apply to open sessions immediately; disabling an account or resetting its password signs it out. At least one enabled admin must remain.
        </p>
        <Button
          onClick={() => {
            setForm({ username: "", password: "", role: "viewer" })
            setCreateOpen(true)
          }}
        >
          Add account
        </Button>
      </div>

      {loading ? (
        <LoadingLine />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Username</TableHead>
              <TableHead className="w-40">Role</TableHead>
              <TableHead>Last sign-in</TableHead>
              <TableHead>Enabled</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {accounts.map((a) => {
              const self = a.id === me.id
              return (
                <TableRow key={a.id}>
                  <TableCell className="font-mono font-medium">
                    {a.username}
                    {self && <span className="text-muted-foreground ml-2 font-sans font-normal">(you)</span>}
                  </TableCell>
                  <TableCell>
                    <RoleSelect value={a.role} disabled={self} onChange={(r) => update(a, { role: r }, `${a.username} is now ${r}`)} />
                  </TableCell>
                  <TableCell className="text-muted-foreground">{a.last_login_at ? relative(a.last_login_at) : "Never"}</TableCell>
                  <TableCell>
                    <Switch
                      checked={a.enabled}
                      disabled={self}
                      onCheckedChange={(v) => update(a, { enabled: v }, `${a.username} ${v ? "enabled" : "disabled"}`)}
                      aria-label={`${a.username} enabled`}
                    />
                  </TableCell>
                  <TableCell className="text-right">
                    {!self && (
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${a.username}`}>
                            <ChevronDown aria-hidden />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem
                            onClick={() => {
                              setResetPass("")
                              setResetTarget(a)
                            }}
                          >
                            Reset password…
                          </DropdownMenuItem>
                          <DropdownMenuItem onClick={() => revokeSessions(a)}>Sign out everywhere</DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem variant="destructive" onClick={() => setDeleteTarget(a)}>
                            Delete
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}

      <Dialog open={createOpen} onOpenChange={(o) => !o && setCreateOpen(false)}>
        <DialogContent className="sm:max-w-[28rem]">
          <DialogHeader>
            <DialogTitle>Add account</DialogTitle>
            <DialogDescription>Share the password with its owner; they can change it under Settings.</DialogDescription>
          </DialogHeader>
          <form id="create-account" onSubmit={create} className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="acct-username">Username</Label>
              <Input id="acct-username" className="font-mono" value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} autoComplete="off" autoFocus />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="acct-password">Password</Label>
              <Input id="acct-password" type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} autoComplete="new-password" />
              <p className="text-muted-foreground text-[0.6875rem] leading-4">At least 8 characters.</p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="acct-role">Role</Label>
              <RoleSelect id="acct-role" value={form.role} onChange={(r) => setForm({ ...form, role: r })} />
              <p className="text-muted-foreground text-[0.6875rem] leading-4">{ROLES.find((r) => r.value === form.role)?.hint}.</p>
            </div>
          </form>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" form="create-account" disabled={saving || !form.username.trim() || !form.password}>
              {saving ? "Creating…" : "Add account"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={!!resetTarget} onOpenChange={(o) => !o && setResetTarget(null)}>
        <DialogContent className="sm:max-w-[26rem]">
          <DialogHeader>
            <DialogTitle>Reset password for {resetTarget?.username}</DialogTitle>
            <DialogDescription>Their open sessions are signed out. Their API keys keep working — revoke them under API keys (All accounts) if the account may be compromised.</DialogDescription>
          </DialogHeader>
          <form id="reset-password" onSubmit={resetPassword} className="space-y-1.5">
            <Label htmlFor="reset-pass">New password</Label>
            <Input id="reset-pass" type="password" value={resetPass} onChange={(e) => setResetPass(e.target.value)} autoComplete="new-password" autoFocus />
          </form>
          <DialogFooter>
            <Button variant="outline" onClick={() => setResetTarget(null)}>
              Cancel
            </Button>
            <Button type="submit" form="reset-password" disabled={!resetPass}>
              Reset password
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {deleteTarget?.username}?</AlertDialogTitle>
            <AlertDialogDescription>Their sessions end and their API keys are deleted with the account.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={remove}>Delete</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Content>
  )
}

// ── Audit log (admin) ───────────────────────────────────────────────────────

const AUDIT_LIMIT = 50

function statusLook(status: number): { tone: "good" | "warning" | "critical" | "muted"; icon: LucideIcon } {
  if (status >= 500) return { tone: "critical", icon: XCircle }
  if (status === 401 || status === 403) return { tone: "warning", icon: AlertTriangle }
  if (status >= 400) return { tone: "muted", icon: CircleMinus }
  return { tone: "good", icon: CheckCircle2 }
}

function AuditTab({ page, actor, action, onChange }: { page: number; actor: string; action: string; onChange: (p: { page?: string; actor?: string; action?: string }) => void }) {
  const [entries, setEntries] = React.useState<AuditEntry[]>([])
  const [total, setTotal] = React.useState(0)
  const [loading, setLoading] = React.useState(true)

  React.useEffect(() => {
    let cancelled = false
    setLoading(true)
    api
      .getAuditLog({ page, limit: AUDIT_LIMIT, actor, action })
      .then((res) => {
        if (cancelled) return
        setEntries(res.entries)
        setTotal(res.total)
      })
      .catch((e) => !cancelled && toast.error(errMsg(e, "Failed to load audit log")))
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
  }, [page, actor, action])

  return (
    <Content>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <SearchInput value={action} onChange={(v) => onChange({ action: v, page: "1" })} placeholder="Filter by action, e.g. DELETE or proxies" />
        {actor && (
          <button type="button" className="text-muted-foreground hover:text-foreground" onClick={() => onChange({ actor: "", page: "1" })}>
            Actor: <span className="font-mono">{actor}</span> ✕
          </button>
        )}
      </div>
      {loading ? (
        <LoadingLine />
      ) : entries.length === 0 ? (
        <EmptyLine>No audit entries match.</EmptyLine>
      ) : (
        <>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Time</TableHead>
                <TableHead>Actor</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Result</TableHead>
                <TableHead>IP</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="text-muted-foreground whitespace-nowrap" title={formatDateTime(e.at)}>
                    {relative(e.at)}
                  </TableCell>
                  <TableCell>
                    <button type="button" className="hover:underline" onClick={() => onChange({ actor: e.actor_name, page: "1" })}>
                      {e.actor_name || "—"}
                    </button>
                    {e.actor_type === "api_key" && <Tag className="ml-2">key</Tag>}
                  </TableCell>
                  <TableCell className="font-mono text-[0.75rem]">{e.action}</TableCell>
                  <TableCell className="text-muted-foreground font-mono text-[0.75rem]">{e.resource || "—"}</TableCell>
                  <TableCell>
                    <StatusLabel {...statusLook(e.status)}>{e.status}</StatusLabel>
                  </TableCell>
                  <TableCell className="text-muted-foreground font-mono text-[0.75rem]">{e.ip || "—"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <Pagination page={page} limit={AUDIT_LIMIT} total={total} onPage={(p) => onChange({ page: String(p) })} />
        </>
      )}
    </Content>
  )
}

// ── Page ────────────────────────────────────────────────────────────────────

const URL_DEFAULTS = { tab: "keys", page: "1", actor: "", action: "" }

function AccessPage() {
  const isAdmin = useCan("admin")
  const [url, setUrl] = useUrlState(URL_DEFAULTS)
  const tab = isAdmin && (url.tab === "accounts" || url.tab === "audit") ? url.tab : "keys"

  return (
    <>
      <PageHeader title="Access" description="Who can sign in, the API keys automation uses, and a record of every change." />
      <TabStrip
        label="Access views"
        tabs={[
          { href: "/dashboard/access", label: "API keys", active: tab === "keys" },
          ...(isAdmin
            ? [
                { href: "/dashboard/access?tab=accounts", label: "Accounts", active: tab === "accounts" },
                { href: "/dashboard/access?tab=audit", label: "Audit log", active: tab === "audit" },
              ]
            : []),
        ]}
      />
      {tab === "keys" && <ApiKeysTab />}
      {tab === "accounts" && <AccountsTab />}
      {tab === "audit" && (
        <AuditTab
          page={Math.max(1, parseInt(url.page) || 1)}
          actor={url.actor}
          action={url.action}
          onChange={(p) => setUrl(p, { replace: p.action !== undefined })}
        />
      )}
    </>
  )
}

export default function Page() {
  return (
    <Suspense fallback={<LoadingLine />}>
      <AccessPage />
    </Suspense>
  )
}
