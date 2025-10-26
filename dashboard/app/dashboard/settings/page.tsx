"use client"

import * as React from "react"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Shield,
  RotateCw,
  Gauge,
  Activity,
  Save,
  Loader2,
  Database,
  ChevronDown,
} from "lucide-react"
import { api } from "@/lib/api"
import { Settings } from "@/lib/types"
import { toast } from "sonner"

export default function SettingsPage() {
  const [settings, setSettings] = React.useState<Settings | null>(null)
  const [isLoading, setIsLoading] = React.useState(true)
  const [isSaving, setIsSaving] = React.useState(false)

  React.useEffect(() => {
    const fetchSettings = async () => {
      try {
        const data = await api.getSettings()
        setSettings(data)
      } catch (error) {
        console.error("Failed to fetch settings:", error)
      } finally {
        setIsLoading(false)
      }
    }

    fetchSettings()
  }, [])

  const handleSave = async () => {
    if (!settings) return

    try {
      setIsSaving(true)
      await api.updateSettings(settings)
      toast.success("Settings saved successfully")
    } catch (error) {
      console.error("Failed to save settings:", error)
      toast.error("Failed to save settings")
    } finally {
      setIsSaving(false)
    }
  }

  const handleReset = async () => {
    if (!confirm("Are you sure you want to reset all settings to defaults?")) return

    try {
      setIsSaving(true)
      const response = await api.resetSettings()
      setSettings(response.config)
      toast.success("Settings reset to defaults")
    } catch (error) {
      console.error("Failed to reset settings:", error)
      toast.error("Failed to reset settings")
    } finally {
      setIsSaving(false)
    }
  }

  if (isLoading || !settings) {
    return (
      <div className="flex items-center justify-center h-96">
        <Loader2 className="h-8 w-8 animate-spin" />
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Settings</h1>
          <p className="text-muted-foreground">
            Configure your Rota proxy rotation system
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="lg" onClick={handleReset} disabled={isSaving}>
            Reset to Defaults
          </Button>
          <Button size="lg" onClick={handleSave} disabled={isSaving}>
            {isSaving ? (
              <>
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                Saving...
              </>
            ) : (
              <>
                <Save className="mr-2 h-4 w-4" />
                Save Configuration
              </>
            )}
          </Button>
        </div>
      </div>

      {/* Proxy Rotation Settings - Full Width (Most Important) */}
      <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <RotateCw className="h-5 w-5" />
              <CardTitle>Proxy Rotation</CardTitle>
            </div>
            <CardDescription>
              Configure proxy rotation strategy and behavior
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div className="grid gap-6 md:grid-cols-2">
              {/* Left Column */}
              <div className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="rotation-method">Rotation Method</Label>
                  <Select
                    value={settings.rotation.method}
                    onValueChange={(value: any) =>
                      setSettings({
                        ...settings,
                        rotation: { ...settings.rotation, method: value },
                      })
                    }
                  >
                    <SelectTrigger id="rotation-method">
                      <SelectValue placeholder="Select method" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="random">Random</SelectItem>
                      <SelectItem value="roundrobin">Round Robin</SelectItem>
                      <SelectItem value="least_conn">Least Connections</SelectItem>
                      <SelectItem value="time_based">Time Based</SelectItem>
                    </SelectContent>
                  </Select>
                </div>

                {settings.rotation.method === "time_based" && (
                  <div className="space-y-2">
                    <Label htmlFor="rotation-interval">Time Based Interval (seconds)</Label>
                    <Input
                      id="rotation-interval"
                      type="number"
                      value={settings.rotation.time_based?.interval || 120}
                      onChange={(e) =>
                        setSettings({
                          ...settings,
                          rotation: {
                            ...settings.rotation,
                            time_based: { interval: parseInt(e.target.value) },
                          },
                        })
                      }
                    />
                  </div>
                )}

                <div className="space-y-2">
                  <Label htmlFor="rotation-timeout">Timeout (seconds)</Label>
                  <Input
                    id="rotation-timeout"
                    type="number"
                    value={settings.rotation.timeout}
                    onChange={(e) =>
                      setSettings({
                        ...settings,
                        rotation: { ...settings.rotation, timeout: parseInt(e.target.value) },
                      })
                    }
                  />
                </div>

                <div className="space-y-2">
                  <Label htmlFor="rotation-retries">Retries</Label>
                  <Input
                    id="rotation-retries"
                    type="number"
                    value={settings.rotation.retries}
                    onChange={(e) =>
                      setSettings({
                        ...settings,
                        rotation: { ...settings.rotation, retries: parseInt(e.target.value) },
                      })
                    }
                  />
                </div>

                <div className="space-y-2">
                  <Label htmlFor="fallback-retries">Fallback Max Retries</Label>
                  <Input
                    id="fallback-retries"
                    type="number"
                    value={settings.rotation.fallback_max_retries}
                    onChange={(e) =>
                      setSettings({
                        ...settings,
                        rotation: { ...settings.rotation, fallback_max_retries: parseInt(e.target.value) },
                      })
                    }
                  />
                </div>

                <div className="space-y-2">
                  <Label htmlFor="max-response-time">Max Response Time (ms)</Label>
                  <Input
                    id="max-response-time"
                    type="number"
                    value={settings.rotation.max_response_time || 0}
                    onChange={(e) =>
                      setSettings({
                        ...settings,
                        rotation: { ...settings.rotation, max_response_time: parseInt(e.target.value) || 0 },
                      })
                    }
                  />
                  <p className="text-xs text-muted-foreground">
                    0 means no limit. Only use proxies faster than this.
                  </p>
                </div>

                <div className="space-y-2">
                  <Label htmlFor="min-success-rate">Min Success Rate (%)</Label>
                  <Input
                    id="min-success-rate"
                    type="number"
                    min="0"
                    max="100"
                    step="1"
                    value={settings.rotation.min_success_rate || 0}
                    onChange={(e) =>
                      setSettings({
                        ...settings,
                        rotation: { ...settings.rotation, min_success_rate: parseFloat(e.target.value) || 0 },
                      })
                    }
                  />
                  <p className="text-xs text-muted-foreground">
                    0 means no minimum. Only use proxies with success rate above this.
                  </p>
                </div>
              </div>

              {/* Right Column */}
              <div className="space-y-4">
                <div className="flex items-center justify-between">
                  <div className="space-y-0.5">
                    <Label htmlFor="remove-unhealthy">Remove Unhealthy</Label>
                    <p className="text-xs text-muted-foreground">
                      Remove unhealthy proxies from rotation
                    </p>
                  </div>
                  <Switch
                    id="remove-unhealthy"
                    checked={settings.rotation.remove_unhealthy}
                    onCheckedChange={(checked) =>
                      setSettings({
                        ...settings,
                        rotation: { ...settings.rotation, remove_unhealthy: checked },
                      })
                    }
                  />
                </div>

                <div className="flex items-center justify-between">
                  <div className="space-y-0.5">
                    <Label htmlFor="fallback">Enable Fallback</Label>
                    <p className="text-xs text-muted-foreground">
                      Continuous operation in case of failures
                    </p>
                  </div>
                  <Switch
                    id="fallback"
                    checked={settings.rotation.fallback}
                    onCheckedChange={(checked) =>
                      setSettings({
                        ...settings,
                        rotation: { ...settings.rotation, fallback: checked },
                      })
                    }
                  />
                </div>

                <div className="flex items-center justify-between">
                  <div className="space-y-0.5">
                    <Label htmlFor="follow-redirect">Follow Redirect</Label>
                    <p className="text-xs text-muted-foreground">
                      Follow HTTP redirections
                    </p>
                  </div>
                  <Switch
                    id="follow-redirect"
                    checked={settings.rotation.follow_redirect}
                    onCheckedChange={(checked) =>
                      setSettings({
                        ...settings,
                        rotation: { ...settings.rotation, follow_redirect: checked },
                      })
                    }
                  />
                </div>

                <div className="space-y-2">
                  <Label>Allowed Protocols</Label>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button variant="outline" className="w-full justify-between">
                        <span>
                          {settings.rotation.allowed_protocols?.length === 5
                            ? "All Protocols"
                            : settings.rotation.allowed_protocols?.length > 0
                            ? `${settings.rotation.allowed_protocols.length} selected`
                            : "Select protocols"}
                        </span>
                        <ChevronDown className="ml-2 h-4 w-4" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent className="w-56">
                      <DropdownMenuLabel>Select Protocols</DropdownMenuLabel>
                      <DropdownMenuSeparator />
                      {["http", "https", "socks4", "socks4a", "socks5"].map((protocol) => (
                        <DropdownMenuCheckboxItem
                          key={protocol}
                          checked={settings.rotation.allowed_protocols?.includes(protocol)}
                          onCheckedChange={(checked) => {
                            const current = settings.rotation.allowed_protocols || [];
                            const updated = checked
                              ? [...current, protocol]
                              : current.filter(p => p !== protocol);
                            setSettings({
                              ...settings,
                              rotation: { ...settings.rotation, allowed_protocols: updated },
                            });
                          }}
                        >
                          {protocol.toUpperCase()}
                        </DropdownMenuCheckboxItem>
                      ))}
                    </DropdownMenuContent>
                  </DropdownMenu>
                  <p className="text-xs text-muted-foreground">
                    Select which protocols to use for proxy rotation
                  </p>
                </div>
              </div>
            </div>
          </CardContent>
        </Card>

      {/* Other Settings in 2-column grid */}
      <div className="grid gap-4 md:grid-cols-2">
        {/* Authentication Settings */}
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <Shield className="h-5 w-5" />
              <CardTitle>Authentication</CardTitle>
            </div>
            <CardDescription>
              Basic authentication settings for your proxy server
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-center justify-between">
              <div className="space-y-0.5">
                <Label htmlFor="auth-enabled">Enable Authentication</Label>
                <p className="text-xs text-muted-foreground">
                  Require username and password for proxy connections
                </p>
              </div>
              <Switch
                id="auth-enabled"
                checked={settings.authentication.enabled}
                onCheckedChange={(checked) =>
                  setSettings({
                    ...settings,
                    authentication: { ...settings.authentication, enabled: checked },
                  })
                }
              />
            </div>
            {settings.authentication.enabled && (
              <>
                <div className="space-y-2">
                  <Label htmlFor="auth-username">Username</Label>
                  <Input
                    id="auth-username"
                    type="text"
                    value={settings.authentication.username}
                    onChange={(e) =>
                      setSettings({
                        ...settings,
                        authentication: { ...settings.authentication, username: e.target.value },
                      })
                    }
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="auth-password">Password</Label>
                  <Input
                    id="auth-password"
                    type="password"
                    placeholder="Enter new password"
                    onChange={(e) =>
                      setSettings({
                        ...settings,
                        authentication: { ...settings.authentication, password: e.target.value },
                      })
                    }
                  />
                  <p className="text-xs text-muted-foreground">
                    Leave empty to keep current password
                  </p>
                </div>
              </>
            )}
          </CardContent>
        </Card>

        {/* Rate Limit Settings */}
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <Gauge className="h-5 w-5" />
              <CardTitle>Rate Limiting</CardTitle>
            </div>
            <CardDescription>
              Control request rate limits
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-center justify-between">
              <div className="space-y-0.5">
                <Label htmlFor="rate-limit-enabled">Enable Rate Limiting</Label>
                <p className="text-xs text-muted-foreground">
                  Limit number of requests per interval
                </p>
              </div>
              <Switch
                id="rate-limit-enabled"
                checked={settings.rate_limit.enabled}
                onCheckedChange={(checked) =>
                  setSettings({
                    ...settings,
                    rate_limit: { ...settings.rate_limit, enabled: checked },
                  })
                }
              />
            </div>

            {settings.rate_limit.enabled && (
              <>
                <div className="space-y-2">
                  <Label htmlFor="rate-limit-interval">Interval (seconds)</Label>
                  <Input
                    id="rate-limit-interval"
                    type="number"
                    value={settings.rate_limit.interval}
                    onChange={(e) =>
                      setSettings({
                        ...settings,
                        rate_limit: { ...settings.rate_limit, interval: parseInt(e.target.value) },
                      })
                    }
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="rate-limit-max">Max Requests per Interval</Label>
                  <Input
                    id="rate-limit-max"
                    type="number"
                    value={settings.rate_limit.max_requests}
                    onChange={(e) =>
                      setSettings({
                        ...settings,
                        rate_limit: { ...settings.rate_limit, max_requests: parseInt(e.target.value) },
                      })
                    }
                  />
                </div>
              </>
            )}
          </CardContent>
        </Card>

        {/* Health Check Settings */}
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <Activity className="h-5 w-5" />
              <CardTitle>Health Check</CardTitle>
            </div>
            <CardDescription>
              Configure proxy health monitoring
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="healthcheck-timeout">Timeout (seconds)</Label>
              <Input
                id="healthcheck-timeout"
                type="number"
                value={settings.healthcheck.timeout}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    healthcheck: { ...settings.healthcheck, timeout: parseInt(e.target.value) },
                  })
                }
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="healthcheck-workers">Number of Workers</Label>
              <Input
                id="healthcheck-workers"
                type="number"
                value={settings.healthcheck.workers}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    healthcheck: { ...settings.healthcheck, workers: parseInt(e.target.value) },
                  })
                }
              />
              <p className="text-xs text-muted-foreground">
                Number of concurrent workers to check proxies
              </p>
            </div>

            <div className="space-y-2">
              <Label htmlFor="healthcheck-url">Health Check URL</Label>
              <Input
                id="healthcheck-url"
                type="url"
                value={settings.healthcheck.url}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    healthcheck: { ...settings.healthcheck, url: e.target.value },
                  })
                }
              />
              <p className="text-xs text-muted-foreground">
                Only GET method is supported
              </p>
            </div>

            <div className="space-y-2">
              <Label htmlFor="healthcheck-status">Expected Status Code</Label>
              <Input
                id="healthcheck-status"
                type="number"
                value={settings.healthcheck.status}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    healthcheck: { ...settings.healthcheck, status: parseInt(e.target.value) },
                  })
                }
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="healthcheck-headers">Headers</Label>
              <Textarea
                id="healthcheck-headers"
                placeholder="Content-Type: application/json&#10;User-Agent: Rota/1.0"
                value={settings.healthcheck.headers.join("\n")}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    healthcheck: {
                      ...settings.healthcheck,
                      headers: e.target.value.split("\n").filter((h) => h.trim()),
                    },
                  })
                }
                rows={4}
                className="font-mono text-sm"
              />
              <p className="text-xs text-muted-foreground">
                One header per line in format: Key: Value
              </p>
            </div>
          </CardContent>
        </Card>

        {/* Log Retention Settings */}
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <Database className="h-5 w-5" />
              <CardTitle>Log Retention</CardTitle>
            </div>
            <CardDescription>
              Configure automatic proxy log cleanup and compression
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-center justify-between">
              <div className="space-y-0.5">
                <Label htmlFor="log-retention-enabled">Enable Auto Cleanup</Label>
                <p className="text-xs text-muted-foreground">
                  Automatically delete old logs based on retention policy
                </p>
              </div>
              <Switch
                id="log-retention-enabled"
                checked={settings.log_retention?.enabled ?? true}
                onCheckedChange={(checked) =>
                  setSettings({
                    ...settings,
                    log_retention: { ...settings.log_retention, enabled: checked },
                  })
                }
              />
            </div>

            {settings.log_retention?.enabled && (
              <>
                <div className="space-y-2">
                  <Label htmlFor="retention-days">Retention Period</Label>
                  <Select
                    value={settings.log_retention.retention_days?.toString() || "30"}
                    onValueChange={(value) =>
                      setSettings({
                        ...settings,
                        log_retention: {
                          ...settings.log_retention,
                          retention_days: parseInt(value),
                        },
                      })
                    }
                  >
                    <SelectTrigger id="retention-days">
                      <SelectValue placeholder="Select retention period" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="7">7 days</SelectItem>
                      <SelectItem value="15">15 days</SelectItem>
                      <SelectItem value="30">30 days (Recommended)</SelectItem>
                      <SelectItem value="60">60 days</SelectItem>
                      <SelectItem value="90">90 days</SelectItem>
                    </SelectContent>
                  </Select>
                  <p className="text-xs text-muted-foreground">
                    Proxy logs older than this will be permanently deleted
                  </p>
                </div>

                <div className="space-y-2">
                  <Label htmlFor="compression-days">Compression After</Label>
                  <Select
                    value={settings.log_retention.compression_after_days?.toString() || "7"}
                    onValueChange={(value) =>
                      setSettings({
                        ...settings,
                        log_retention: {
                          ...settings.log_retention,
                          compression_after_days: parseInt(value),
                        },
                      })
                    }
                  >
                    <SelectTrigger id="compression-days">
                      <SelectValue placeholder="Select compression period" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="1">1 day</SelectItem>
                      <SelectItem value="3">3 days</SelectItem>
                      <SelectItem value="7">7 days (Recommended)</SelectItem>
                      <SelectItem value="14">14 days</SelectItem>
                    </SelectContent>
                  </Select>
                  <p className="text-xs text-muted-foreground">
                    Logs older than this will be compressed to save space
                  </p>
                </div>

                <div className="space-y-2">
                  <Label htmlFor="cleanup-interval">Cleanup Interval</Label>
                  <Select
                    value={settings.log_retention.cleanup_interval_hours?.toString() || "24"}
                    onValueChange={(value) =>
                      setSettings({
                        ...settings,
                        log_retention: {
                          ...settings.log_retention,
                          cleanup_interval_hours: parseInt(value),
                        },
                      })
                    }
                  >
                    <SelectTrigger id="cleanup-interval">
                      <SelectValue placeholder="Select cleanup interval" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="1">Every 1 hour</SelectItem>
                      <SelectItem value="6">Every 6 hours</SelectItem>
                      <SelectItem value="12">Every 12 hours</SelectItem>
                      <SelectItem value="24">Every 24 hours (Recommended)</SelectItem>
                    </SelectContent>
                  </Select>
                  <p className="text-xs text-muted-foreground">
                    How often to run the cleanup job
                  </p>
                </div>

                <div className="rounded-lg bg-muted p-3 text-sm">
                  <p className="font-medium mb-1">Current Configuration:</p>
                  <ul className="space-y-1 text-muted-foreground">
                    <li>• Logs kept for {settings.log_retention.retention_days} days</li>
                    <li>• Compressed after {settings.log_retention.compression_after_days} days</li>
                    <li>• Cleanup runs every {settings.log_retention.cleanup_interval_hours} hours</li>
                  </ul>
                </div>
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
