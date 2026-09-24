"use client"

import * as React from "react"
import { Eye, EyeOff } from "lucide-react"
import { toast } from "@/lib/toast"
import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { Checkbox } from "@/components/ui/checkbox"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
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
import { PageHeader, Section, LoadingLine } from "@/components/page-header"
import { api } from "@/lib/api"
import { useCan, useSession } from "@/lib/session"
import { Settings } from "@/lib/types"
import { formatDateTime } from "@/lib/format"
import { cn } from "@/lib/utils"

const PROTOCOLS = ["http", "https", "socks4", "socks4a", "socks5"]

/** Label + control + one-line hint, in a form grid. */
function Field({
  id,
  label,
  hint,
  children,
  className,
}: {
  id?: string
  label: React.ReactNode
  hint?: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && <p className="text-muted-foreground text-[0.6875rem] leading-4">{hint}</p>}
    </div>
  )
}

/** Boolean setting as a row: what it does on the left, the switch on the right. */
function SwitchRow({
  id,
  label,
  hint,
  checked,
  onChange,
}: {
  id: string
  label: React.ReactNode
  hint?: React.ReactNode
  checked: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <div className="border-border flex items-start justify-between gap-6 border-b py-3 last:border-b-0">
      <div className="min-w-0">
        <Label htmlFor={id} className="text-foreground cursor-pointer">
          {label}
        </Label>
        {hint && <p className="text-muted-foreground mt-1 text-[0.6875rem] leading-4">{hint}</p>}
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onChange} />
    </div>
  )
}

const grid = "grid gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-3"

export default function SettingsPage() {
  const me = useSession()
  const isAdmin = useCan("admin")
  const [settings, setSettings] = React.useState<Settings | null>(null)
  const [isLoading, setIsLoading] = React.useState(true)
  const [isSaving, setIsSaving] = React.useState(false)
  const [resetOpen, setResetOpen] = React.useState(false)

  // Admin account
  const [adminUsername, setAdminUsername] = React.useState("")
  const [newUsername, setNewUsername] = React.useState("")
  const [currentPass, setCurrentPass] = React.useState("")
  const [newPass, setNewPass] = React.useState("")
  const [confirmPass, setConfirmPass] = React.useState("")
  const [showPass, setShowPass] = React.useState(false)
  const [changingPass, setChangingPass] = React.useState(false)
  const [isUpdatingGeoDB, setIsUpdatingGeoDB] = React.useState(false)
  // Raw textarea text; parsed into header lines on change so a trailing newline survives typing.
  const [headersText, setHeadersText] = React.useState<string | null>(null)

  React.useEffect(() => {
    const fetchSettings = async () => {
      try {
        setSettings(await api.getSettings())
        setAdminUsername(me.username)
        setNewUsername(me.username)
      } catch (error) {
        console.error("Failed to fetch settings:", error)
      } finally {
        setIsLoading(false)
      }
    }
    fetchSettings()
  }, [me.username])

  const patch = <K extends keyof Settings>(key: K, value: Partial<Settings[K]>) =>
    setSettings((s) => (s ? { ...s, [key]: { ...s[key], ...value } } : s))

  const handleChangePassword = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!currentPass) return toast.error("Enter your current password")
    if (!newPass) return toast.error("Enter a new password")
    if (newPass.length < 8) return toast.error("New password must be at least 8 characters")
    if (newPass !== confirmPass) return toast.error("Passwords don't match")

    setChangingPass(true)
    try {
      const opts: { current_password: string; new_password: string; new_username?: string } = {
        current_password: currentPass,
        new_password: newPass,
      }
      if (newUsername && newUsername !== adminUsername) opts.new_username = newUsername
      const res = await api.changePassword(opts)
      setAdminUsername(res.username)
      setNewUsername(res.username)
      setCurrentPass("")
      setNewPass("")
      setConfirmPass("")
      toast.success("Credentials updated", "Your other sessions were signed out")
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to change password")
    } finally {
      setChangingPass(false)
    }
  }

  const handleSave = async () => {
    if (!settings) return
    try {
      setIsSaving(true)
      await api.updateSettings(settings)
      toast.success("Settings saved", "The core applied them without a restart")
    } catch {
      toast.error("Failed to save settings")
    } finally {
      setIsSaving(false)
    }
  }

  const handleReset = async () => {
    try {
      setIsSaving(true)
      const response = await api.resetSettings()
      setSettings(response.config)
      setHeadersText(null)
      toast.success("Settings reset to defaults")
    } catch {
      toast.error("Failed to reset settings")
    } finally {
      setIsSaving(false)
      setResetOpen(false)
    }
  }

  const handleUpdateGeoDB = async () => {
    try {
      setIsUpdatingGeoDB(true)
      const res = await api.updateGeoIPDB()
      toast.success(res.message || "GeoIP database updated")
      setSettings(await api.getSettings())
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Failed to update GeoIP database")
    } finally {
      setIsUpdatingGeoDB(false)
    }
  }

  if (isLoading || !settings) return <LoadingLine />

  const num = (v: string, fallback = 0) => {
    const n = parseInt(v)
    return Number.isNaN(n) ? fallback : n
  }

  return (
    <>
      <PageHeader
        title="Settings"
        description={
          isAdmin
            ? "Runtime configuration of the core. Saving applies everything below at once; your account section saves on its own."
            : "Runtime configuration of the core. Only admins can change it; you can still update your own account below."
        }
      >
        {isAdmin && (
          <>
            <Button variant="outline" onClick={() => setResetOpen(true)} disabled={isSaving}>
              Reset to defaults
            </Button>
            <Button onClick={handleSave} disabled={isSaving}>
              {isSaving ? "Saving…" : "Save settings"}
            </Button>
          </>
        )}
      </PageHeader>

      {/* Own account */}
      <Section
        title="Your account"
        description={
          <>
            Signed in as <span className="font-mono">{adminUsername}</span> with the <span className="font-medium">{me.role}</span> role. Changing the password signs your other sessions out.
          </>
        }
      >
        <form onSubmit={handleChangePassword} className="max-w-2xl">
          <div className={grid}>
            <Field id="admin-username" label="Username" hint="Leave unchanged to keep the current one.">
              <Input id="admin-username" className="font-mono" value={newUsername} onChange={(e) => setNewUsername(e.target.value)} autoComplete="username" />
            </Field>
            <Field id="admin-current" label="Current password" hint="Required to confirm any change.">
              <div className="relative">
                <Input id="admin-current" type={showPass ? "text" : "password"} value={currentPass} onChange={(e) => setCurrentPass(e.target.value)} className="pr-8" autoComplete="current-password" />
                <button
                  type="button"
                  className="text-muted-foreground hover:text-foreground absolute top-1/2 right-2 -translate-y-1/2"
                  onClick={() => setShowPass((v) => !v)}
                  aria-label={showPass ? "Hide passwords" : "Show passwords"}
                >
                  {showPass ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                </button>
              </div>
            </Field>
            <div className="hidden lg:block" />
            <Field id="admin-new" label="New password" hint="At least 8 characters.">
              <Input id="admin-new" type={showPass ? "text" : "password"} value={newPass} onChange={(e) => setNewPass(e.target.value)} autoComplete="new-password" />
            </Field>
            <Field id="admin-confirm" label="Confirm new password">
              <Input id="admin-confirm" type={showPass ? "text" : "password"} value={confirmPass} onChange={(e) => setConfirmPass(e.target.value)} autoComplete="new-password" />
            </Field>
          </div>
          <Button type="submit" variant="outline" className="mt-4" disabled={changingPass || !currentPass || !newPass}>
            {changingPass ? "Updating…" : "Update credentials"}
          </Button>
        </form>
      </Section>

      {/* Rotation */}
      <Section title="Rotation" description="How the global rotation (users without a pool) picks and retries proxies.">
        <div className={grid}>
          <Field id="rotation-method" label="Method">
            <Select value={settings.rotation.method} onValueChange={(v) => patch("rotation", { method: v as Settings["rotation"]["method"] })}>
              <SelectTrigger id="rotation-method" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="random">Random</SelectItem>
                <SelectItem value="roundrobin">Round robin</SelectItem>
                <SelectItem value="least_conn">Least connections</SelectItem>
                <SelectItem value="time_based">Time based</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          {settings.rotation.method === "time_based" && (
            <Field id="rotation-interval" label="Rotate every (seconds)">
              <Input id="rotation-interval" type="number" min={1} value={settings.rotation.time_based?.interval || 120} onChange={(e) => patch("rotation", { time_based: { interval: num(e.target.value, 120) } })} />
            </Field>
          )}
          <Field id="rotation-timeout" label="Timeout (seconds)" hint="Per upstream attempt.">
            <Input id="rotation-timeout" type="number" min={1} value={settings.rotation.timeout} onChange={(e) => patch("rotation", { timeout: num(e.target.value) })} />
          </Field>
          <Field id="rotation-retries" label="Retries" hint="Different proxy on each retry.">
            <Input id="rotation-retries" type="number" min={0} value={settings.rotation.retries} onChange={(e) => patch("rotation", { retries: num(e.target.value) })} />
          </Field>
          <Field id="fallback-retries" label="Fallback max retries">
            <Input id="fallback-retries" type="number" min={0} value={settings.rotation.fallback_max_retries} onChange={(e) => patch("rotation", { fallback_max_retries: num(e.target.value) })} />
          </Field>
          <Field id="max-response-time" label="Max response time (ms)" hint="0 = no limit. Only proxies faster than this are used.">
            <Input id="max-response-time" type="number" min={0} value={settings.rotation.max_response_time || 0} onChange={(e) => patch("rotation", { max_response_time: num(e.target.value) })} />
          </Field>
          <Field id="min-success-rate" label="Min success rate (%)" hint="0 = no minimum. Only proxies above this are used.">
            <Input id="min-success-rate" type="number" min={0} max={100} step={1} value={settings.rotation.min_success_rate || 0} onChange={(e) => patch("rotation", { min_success_rate: parseFloat(e.target.value) || 0 })} />
          </Field>
          <Field label="Allowed protocols" hint={(settings.rotation.allowed_protocols?.length ?? 0) === 0 ? "None checked = every protocol is allowed." : "Proxies of other protocols stay in the inventory but are never picked."} className="sm:col-span-2 lg:col-span-1">
            <div className="flex flex-wrap gap-x-4 gap-y-2 pt-1">
              {PROTOCOLS.map((p) => {
                const current = settings.rotation.allowed_protocols || []
                const on = current.includes(p)
                return (
                  <label key={p} className="inline-flex cursor-pointer items-center gap-1.5 font-mono">
                    <Checkbox
                      checked={on}
                      onCheckedChange={(v) => patch("rotation", { allowed_protocols: v ? [...current, p] : current.filter((x) => x !== p) })}
                    />
                    {p}
                  </label>
                )
              })}
            </div>
          </Field>
        </div>
        <div className="mt-6 max-w-2xl">
          <SwitchRow id="remove-unhealthy" label="Remove unhealthy proxies" hint="Failed proxies leave the rotation until the next health check passes." checked={settings.rotation.remove_unhealthy} onChange={(v) => patch("rotation", { remove_unhealthy: v })} />
          <SwitchRow id="fallback" label="Fall back on failure" hint="Keep serving through another proxy when the chosen one fails." checked={settings.rotation.fallback} onChange={(v) => patch("rotation", { fallback: v })} />
          <SwitchRow id="follow-redirect" label="Follow redirects" hint="Resolve 3xx responses upstream instead of passing them to the client." checked={settings.rotation.follow_redirect} onChange={(v) => patch("rotation", { follow_redirect: v })} />
        </div>
      </Section>

      {/* Authentication */}
      <Section title="Proxy authentication" description="A single shared credential for the proxy port, in addition to per-user accounts.">
        <div className="max-w-2xl">
          <SwitchRow id="auth-enabled" label="Require the shared credential" hint="When off, only user accounts (or nothing) gate the proxy." checked={settings.authentication.enabled} onChange={(v) => patch("authentication", { enabled: v })} />
        </div>
        {settings.authentication.enabled && (
          <div className={cn(grid, "mt-4")}>
            <Field id="auth-username" label="Username">
              <Input id="auth-username" className="font-mono" value={settings.authentication.username} onChange={(e) => patch("authentication", { username: e.target.value })} />
            </Field>
            <Field id="auth-password" label="Password" hint="Leave empty to keep the current one.">
              <Input id="auth-password" type="password" placeholder="••••••••" onChange={(e) => patch("authentication", { password: e.target.value })} />
            </Field>
          </div>
        )}
      </Section>

      {/* Rate limiting */}
      <Section title="Rate limiting" description="Global cap on requests through the proxy port, per client IP.">
        <div className="max-w-2xl">
          <SwitchRow id="rate-limit-enabled" label="Enable rate limiting" hint="Over the cap, the proxy answers 429." checked={settings.rate_limit.enabled} onChange={(v) => patch("rate_limit", { enabled: v })} />
        </div>
        {settings.rate_limit.enabled && (
          <div className={cn(grid, "mt-4")}>
            <Field id="rate-limit-interval" label="Window (seconds)">
              <Input id="rate-limit-interval" type="number" min={1} value={settings.rate_limit.interval} onChange={(e) => patch("rate_limit", { interval: num(e.target.value) })} />
            </Field>
            <Field id="rate-limit-max" label="Max requests per window">
              <Input id="rate-limit-max" type="number" min={1} value={settings.rate_limit.max_requests} onChange={(e) => patch("rate_limit", { max_requests: num(e.target.value) })} />
            </Field>
          </div>
        )}
      </Section>

      {/* Health check */}
      <Section title="Health check" description="The GET each proxy must pass to count as active.">
        <div className={grid}>
          <Field id="healthcheck-url" label="URL" hint="GET only." className="sm:col-span-2">
            <Input id="healthcheck-url" className="font-mono" value={settings.healthcheck.url} onChange={(e) => patch("healthcheck", { url: e.target.value })} />
          </Field>
          <Field id="healthcheck-status" label="Expected status">
            <Input id="healthcheck-status" type="number" min={100} max={599} value={settings.healthcheck.status} onChange={(e) => patch("healthcheck", { status: num(e.target.value, 200) })} />
          </Field>
          <Field id="healthcheck-timeout" label="Timeout (seconds)">
            <Input id="healthcheck-timeout" type="number" min={1} value={settings.healthcheck.timeout} onChange={(e) => patch("healthcheck", { timeout: num(e.target.value) })} />
          </Field>
          <Field id="healthcheck-workers" label="Workers" hint="Concurrent checks.">
            <Input id="healthcheck-workers" type="number" min={1} value={settings.healthcheck.workers} onChange={(e) => patch("healthcheck", { workers: num(e.target.value) })} />
          </Field>
          <Field id="healthcheck-headers" label="Headers" hint="One per line, as Key: Value." className="sm:col-span-2 lg:col-span-3">
            <Textarea
              id="healthcheck-headers"
              rows={3}
              className="max-w-2xl font-mono text-[0.75rem]"
              placeholder={"User-Agent: Rota/1.0"}
              value={headersText ?? settings.healthcheck.headers.join("\n")}
              onChange={(e) => {
                setHeadersText(e.target.value)
                patch("healthcheck", { headers: e.target.value.split("\n").filter((h) => h.trim()) })
              }}
            />
          </Field>
        </div>
        <div className="mt-4 max-w-2xl">
          <SwitchRow id="healthcheck-strict-tls" label="Strict TLS" hint="Reject proxies presenting expired or invalid certificates." checked={settings.healthcheck.strict_tls ?? false} onChange={(v) => patch("healthcheck", { strict_tls: v })} />
        </div>
      </Section>

      {/* Log retention */}
      <Section title="Log retention" description="Automatic cleanup of proxy request logs in TimescaleDB.">
        <div className="max-w-2xl">
          <SwitchRow id="log-retention-enabled" label="Delete old logs automatically" hint="Without it, request logs grow until you prune them by hand." checked={settings.log_retention.enabled} onChange={(v) => patch("log_retention", { enabled: v })} />
        </div>
        {settings.log_retention.enabled && (
          <div className={cn(grid, "mt-4")}>
            <Field id="retention-days" label="Keep logs for" hint="Older rows are deleted for good.">
              <Select value={String(settings.log_retention.retention_days)} onValueChange={(v) => patch("log_retention", { retention_days: num(v, 30) })}>
                <SelectTrigger id="retention-days" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {[7, 15, 30, 60, 90].map((d) => (
                    <SelectItem key={d} value={String(d)}>{d} days</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field id="compression-days" label="Compress after" hint="Compressed chunks read slower but take a fraction of the space.">
              <Select value={String(settings.log_retention.compression_after_days)} onValueChange={(v) => patch("log_retention", { compression_after_days: num(v, 7) })}>
                <SelectTrigger id="compression-days" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {[1, 3, 7, 14].map((d) => (
                    <SelectItem key={d} value={String(d)}>{d} {d === 1 ? "day" : "days"}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field id="log-cleanup-interval" label="Run cleanup every">
              <Select value={String(settings.log_retention.cleanup_interval_hours)} onValueChange={(v) => patch("log_retention", { cleanup_interval_hours: num(v, 24) })}>
                <SelectTrigger id="log-cleanup-interval" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {[1, 6, 12, 24].map((h) => (
                    <SelectItem key={h} value={String(h)}>{h} {h === 1 ? "hour" : "hours"}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>
        )}
      </Section>

      {/* Proxy cleanup */}
      <Section title="Proxy cleanup" description="Automatic removal of proxies that keep failing or perform badly. Runs on the interval below; removed proxies come back only if a source lists them again.">
        <div className="max-w-2xl">
          <SwitchRow
            id="proxy-cleanup-enabled"
            label="Remove dead proxies automatically"
            hint="Off means the inventory only shrinks when you delete proxies yourself or a source's own cleanup runs."
            checked={settings.proxy_cleanup?.enabled ?? false}
            onChange={(v) => patch("proxy_cleanup", { enabled: v })}
          />
        </div>
        {settings.proxy_cleanup?.enabled && (
          <div className={cn(grid, "mt-4")}>
            <Field id="cleanup-failed-days" label="Failed for longer than (days)" hint="Proxies whose last successful check is older than this are deleted.">
              <Input id="cleanup-failed-days" type="number" min={1} value={settings.proxy_cleanup.max_failed_days} onChange={(e) => patch("proxy_cleanup", { max_failed_days: num(e.target.value, 7) })} />
            </Field>
            <Field id="cleanup-min-success" label="Min success rate (%)" hint="0 = ignore success rate. Otherwise proxies below it are deleted too.">
              <Input id="cleanup-min-success" type="number" min={0} max={100} value={settings.proxy_cleanup.min_success_rate} onChange={(e) => patch("proxy_cleanup", { min_success_rate: parseFloat(e.target.value) || 0 })} />
            </Field>
            <Field id="cleanup-interval" label="Run every">
              <Select value={String(settings.proxy_cleanup.cleanup_interval_hours)} onValueChange={(v) => patch("proxy_cleanup", { cleanup_interval_hours: num(v, 24) })}>
                <SelectTrigger id="cleanup-interval" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {[1, 6, 12, 24, 48].map((h) => (
                    <SelectItem key={h} value={String(h)}>{h} {h === 1 ? "hour" : "hours"}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>
        )}
      </Section>

      {/* GeoIP */}
      <Section
        title="GeoIP"
        description="Where country, city and ISP for each proxy come from. Pools filter on this data."
        className="border-b-0"
        actions={
          settings.geoip?.provider === "maxmind" && (
            <Button variant="outline" size="sm" onClick={handleUpdateGeoDB} disabled={!isAdmin || isUpdatingGeoDB || isSaving}>
              {isUpdatingGeoDB ? "Downloading…" : "Download database now"}
            </Button>
          )
        }
      >
        <div className={grid}>
          <Field
            id="geoip-provider"
            label="Provider"
            hint={settings.geoip?.provider === "maxmind" ? "Local MMDB file: instant lookups, no rate limit." : "ip-api.com batch endpoint; the free tier is rate limited."}
          >
            <Select
              value={settings.geoip?.provider || "ip-api"}
              onValueChange={(value: "ip-api" | "maxmind") =>
                setSettings({
                  ...settings,
                  geoip: {
                    provider: value,
                    maxmind_license_key: settings.geoip?.maxmind_license_key || "",
                    maxmind_db_path: settings.geoip?.maxmind_db_path || "data/GeoLite2-City.mmdb",
                    maxmind_url: settings.geoip?.maxmind_url || "",
                    auto_update: settings.geoip?.auto_update ?? false,
                    update_interval_hours: settings.geoip?.update_interval_hours || 168,
                    last_updated_at: settings.geoip?.last_updated_at,
                  },
                })
              }
            >
              <SelectTrigger id="geoip-provider" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="ip-api">ip-api.com (web API)</SelectItem>
                <SelectItem value="maxmind">MaxMind GeoLite2 (local MMDB)</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          {settings.geoip?.provider === "maxmind" && (
            <>
              <Field id="maxmind-key" label="MaxMind license key" hint="Needed to download the official GeoLite2-City database.">
                <Input id="maxmind-key" type={showPass ? "text" : "password"} className="font-mono" value={settings.geoip?.maxmind_license_key || ""} onChange={(e) => patch("geoip", { maxmind_license_key: e.target.value })} />
              </Field>
              <Field id="maxmind-path" label="Database path" hint="Where the .mmdb file is stored.">
                <Input id="maxmind-path" className="font-mono" value={settings.geoip?.maxmind_db_path || "data/GeoLite2-City.mmdb"} onChange={(e) => patch("geoip", { maxmind_db_path: e.target.value })} />
              </Field>
              <Field id="maxmind-url" label="Download URL" hint="Falls back to the P3TERX daily mirror when no license key is set." className="sm:col-span-2">
                <Input
                  id="maxmind-url"
                  type="url"
                  className="font-mono"
                  value={settings.geoip?.maxmind_url ?? "https://raw.githubusercontent.com/P3TERX/GeoLite.mmdb/download/GeoLite2-City.mmdb"}
                  onChange={(e) => patch("geoip", { maxmind_url: e.target.value })}
                />
              </Field>
              {settings.geoip?.auto_update && (
                <Field id="update-interval" label="Update every (hours)" hint="168 = weekly.">
                  <Input id="update-interval" type="number" min={1} value={settings.geoip?.update_interval_hours || 168} onChange={(e) => patch("geoip", { update_interval_hours: num(e.target.value, 168) })} />
                </Field>
              )}
            </>
          )}
        </div>
        {settings.geoip?.provider === "maxmind" && (
          <div className="mt-4 max-w-2xl">
            <SwitchRow id="geoip-autoupdate" label="Update the database automatically" hint={settings.geoip?.last_updated_at ? `Last updated ${formatDateTime(settings.geoip.last_updated_at)}.` : "Never downloaded yet."} checked={settings.geoip?.auto_update ?? false} onChange={(v) => patch("geoip", { auto_update: v })} />
          </div>
        )}
      </Section>

      <AlertDialog open={resetOpen} onOpenChange={setResetOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Reset every setting to its default?</AlertDialogTitle>
            <AlertDialogDescription>The core switches to defaults immediately. The admin account and proxy users are not touched.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={handleReset}>Reset</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
