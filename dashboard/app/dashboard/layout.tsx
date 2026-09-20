"use client"

import * as React from "react"
import { useRouter } from "next/navigation"
import { AppNav } from "@/components/shell/app-nav"
import { api } from "@/lib/api"

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter()
  const [username, setUsername] = React.useState<string | null>(null)
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
      .getAdminInfo()
      .then((info) => {
        if (cancelled) return
        setUsername(info.username)
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

  if (isLoading) {
    return <p className="text-muted-foreground grid min-h-svh place-items-center">Checking session…</p>
  }

  if (username === null) return null

  return (
    <div className="md:grid md:min-h-svh md:grid-cols-[14.5rem_1fr]">
      {/* Column carries the background so it spans pages taller than the viewport. */}
      <div className="bg-sidebar border-border relative md:border-r">
        <AppNav username={username} />
      </div>
      <main className="flex min-w-0 flex-col">{children}</main>
    </div>
  )
}
