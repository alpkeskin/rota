"use client"

import * as React from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"

/**
 * Filters, sorting and paging live in the URL so a screen is a shareable
 * link and the back button works. Values equal to their default are dropped
 * from the query string to keep URLs short.
 */
export function useUrlState<T extends Record<string, string>>(defaults: T) {
  const router = useRouter()
  const pathname = usePathname()
  const searchParams = useSearchParams()

  const state = React.useMemo(() => {
    const out = { ...defaults }
    for (const key of Object.keys(defaults) as (keyof T)[]) {
      const v = searchParams.get(key as string)
      if (v !== null) (out as Record<string, string>)[key as string] = v
    }
    return out
  }, [searchParams, defaults])

  const set = React.useCallback(
    (patch: Partial<T>, opts?: { replace?: boolean }) => {
      const next = new URLSearchParams(searchParams.toString())
      for (const [key, value] of Object.entries(patch)) {
        if (value === undefined || value === null || value === defaults[key]) {
          next.delete(key)
        } else {
          next.set(key, String(value))
        }
      }
      const qs = next.toString()
      const url = qs ? `${pathname}?${qs}` : pathname
      if (opts?.replace) router.replace(url, { scroll: false })
      else router.push(url, { scroll: false })
    },
    [searchParams, pathname, router, defaults]
  )

  return [state, set] as const
}
