"use client"

import * as React from "react"
import { useRouter } from "next/navigation"
import { useTheme } from "next-themes"
import { Check, ChevronsUpDown, LogOut } from "lucide-react"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { api } from "@/lib/api"
import { cn } from "@/lib/utils"

export function AccountMenu({ username, compact }: { username: string; compact?: boolean }) {
  const router = useRouter()
  const { theme, setTheme } = useTheme()
  const [mounted, setMounted] = React.useState(false)
  React.useEffect(() => setMounted(true), [])

  const signOut = () => {
    api.clearToken()
    router.push("/login")
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className={cn(
          "hover:bg-sidebar-accent/60 flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left font-medium transition-colors",
          "focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
          compact && "w-auto"
        )}
      >
        <span className="bg-muted text-muted-foreground flex size-5 shrink-0 items-center justify-center rounded text-[0.625rem] font-semibold uppercase">
          {username.slice(0, 1) || "?"}
        </span>
        {!compact && <span className="truncate">{username || "—"}</span>}
        <ChevronsUpDown className="text-muted-foreground ml-auto size-3.5 shrink-0" aria-hidden />
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="w-56">
        <div className="px-2 py-1.5">
          <p className="font-medium">{username}</p>
          <p className="text-muted-foreground font-mono">@{username}</p>
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>Appearance</DropdownMenuLabel>
        {(["light", "dark", "system"] as const).map((t) => (
          <DropdownMenuItem key={t} onClick={() => setTheme(t)}>
            <span className="capitalize">{t}</span>
            {mounted && theme === t && <Check className="ml-auto size-3.5" aria-hidden />}
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={signOut}>
          <LogOut aria-hidden />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
