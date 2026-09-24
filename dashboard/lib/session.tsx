"use client"

import * as React from "react"
import type { Me, Role } from "@/lib/types"

const RANK: Record<Role, number> = { viewer: 1, operator: 2, admin: 3 }

/** Whether role grants everything min grants. */
export function roleAtLeast(role: Role | undefined, min: Role): boolean {
  return !!role && RANK[role] >= RANK[min]
}

const SessionContext = React.createContext<Me | null>(null)

export function SessionProvider({ me, children }: { me: Me; children: React.ReactNode }) {
  return <SessionContext.Provider value={me}>{children}</SessionContext.Provider>
}

/** The signed-in account. Only valid inside the dashboard layout. */
export function useSession(): Me {
  const me = React.useContext(SessionContext)
  if (!me) throw new Error("useSession must be used inside SessionProvider")
  return me
}

/** Whether the signed-in account has at least the given role. */
export function useCan(min: Role): boolean {
  return roleAtLeast(useSession().role, min)
}
