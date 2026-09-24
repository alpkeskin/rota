"use client"

import * as React from "react"
import { useRouter } from "next/navigation"
import { AppNav } from "@/components/shell/app-nav"
import { api } from "@/lib/api"
import { SessionProvider } from "@/lib/session"
import type { Me } from "@/lib/types"

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter()
  const [me, setMe] = React.useState<Me | null>(null)
  const [isLoading, setIsLoading] = React.useState(true)

  // Validate the stored token against the backend on mount so a stale/expired
  // token can't flash protected content before a later 401 bounce.
  React.useEffect(() => {
    let cancelled = false

    const token = localStorage.getItem("auth_token")
    if (!token) {
      router.push("/login")
      setIsLoading(false)
      return
    }

    api
      .getMe()
      .then((info) => {
        if (cancelled) return
        setMe(info)
      })
      .catch(() => {
        if (cancelled) return
        api.clearToken()
        router.push("/login")
      })
      .finally(() => {
        if (!cancelled) setIsLoading(false)
      })

    return () => {
      cancelled = true
    }
  }, [router])

  // Re-read the account when the tab regains focus, so a role another admin
  // changed (or a revoked session) shows up without a manual reload.
  const refresh = React.useCallback(async () => {
    try {
      setMe(await api.getMe())
    } catch {
      // A 401 already redirects to the login page inside the API client.
    }
  }, [])
  React.useEffect(() => {
    const onFocus = () => {
      if (document.visibilityState === "visible") refresh()
    }
    window.addEventListener("focus", onFocus)
    document.addEventListener("visibilitychange", onFocus)
    return () => {
      window.removeEventListener("focus", onFocus)
      document.removeEventListener("visibilitychange", onFocus)
    }
  }, [refresh])

  if (isLoading) {
    return <p className="text-muted-foreground grid min-h-svh place-items-center">Checking session…</p>
  }

  if (me === null) return null

  return (
    <SessionProvider me={me} refresh={refresh}>
      <div className="md:grid md:min-h-svh md:grid-cols-[14.5rem_1fr]">
        {/* Column carries the background so it spans pages taller than the viewport. */}
        <div className="bg-sidebar border-border relative md:border-r">
          <AppNav username={me.username} role={me.role} />
        </div>
        <main className="flex min-w-0 flex-col">
          {me.role === "viewer" && (
            <p className="border-border bg-muted/40 text-muted-foreground border-b px-4 py-1.5 md:px-6">
              Read-only access — your account has the viewer role, so changes will be refused.
            </p>
          )}
          {children}
        </main>
      </div>
    </SessionProvider>
  )
}
