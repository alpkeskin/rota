"use client"

import * as React from "react"
import type { Me, Role } from "@/lib/types"

const RANK: Record<Role, number> = { viewer: 1, operator: 2, admin: 3 }

/** Whether role grants everything min grants. */
export function roleAtLeast(role: Role | undefined, min: Role): boolean {
  return !!role && RANK[role] >= RANK[min]
}

interface SessionValue {
  me: Me
  /** Re-reads the account (after renaming it, or when a role may have changed). */
  refresh: () => Promise<void>
}

const SessionContext = React.createContext<SessionValue | null>(null)

export function SessionProvider({ me, refresh, children }: SessionValue & { children: React.ReactNode }) {
  const value = React.useMemo(() => ({ me, refresh }), [me, refresh])
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}

function useSessionValue(): SessionValue {
  const v = React.useContext(SessionContext)
  if (!v) throw new Error("useSession must be used inside SessionProvider")
  return v
}

/** The signed-in account. Only valid inside the dashboard layout. */
export function useSession(): Me {
  return useSessionValue().me
}

/** Re-fetches the signed-in account into the session. */
export function useRefreshSession(): () => Promise<void> {
  return useSessionValue().refresh
}

/** Whether the signed-in account has at least the given role. */
export function useCan(min: Role): boolean {
  return roleAtLeast(useSession().role, min)
}
