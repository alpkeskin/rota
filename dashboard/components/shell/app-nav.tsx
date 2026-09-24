"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Activity,
  FileText,
  Gauge,
  KeyRound,
  Layers,
  Network,
  Rss,
  Settings,
  Users,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { AccountMenu } from "@/components/shell/account-menu";

// Order follows the workflow: what happened → the inventory → how it is
// grouped and who uses it → raw records → configuration. Not alphabetical.
const NAV: {
  href: string;
  label: string;
  icon: LucideIcon;
  exact?: boolean;
}[] = [
  { href: "/dashboard", label: "Overview", icon: Gauge, exact: true },
  { href: "/dashboard/proxies", label: "Proxies", icon: Network },
  { href: "/dashboard/sources", label: "Sources", icon: Rss },
  { href: "/dashboard/pools", label: "Pools", icon: Layers },
  { href: "/dashboard/users", label: "Users", icon: Users },
  { href: "/dashboard/logs", label: "Logs", icon: FileText },
  { href: "/dashboard/metrics", label: "System", icon: Activity },
  { href: "/dashboard/access", label: "Access", icon: KeyRound },
  { href: "/dashboard/settings", label: "Settings", icon: Settings },
];

export function AppNav({ username, role }: { username: string; role: string }) {
  const pathname = usePathname();
  const tz = React.useMemo(() => {
    try {
      const name = Intl.DateTimeFormat().resolvedOptions().timeZone;
      const offset = -new Date().getTimezoneOffset() / 60;
      const sign = offset >= 0 ? "+" : "−";
      return `${name.split("/").pop()?.replace("_", " ") ?? name} (GMT${sign}${Math.abs(offset)})`;
    } catch {
      return "local time";
    }
  }, []);

  return (
    <aside className="border-border flex flex-col gap-4 border-b px-3 py-3 md:sticky md:top-0 md:h-svh md:border-b-0 md:py-4">
      <div className="flex items-center gap-2">
        <Link
          href="/dashboard"
          className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1 focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none"
        >
          {/* One white asset, tinted with the foreground color so it works in both themes. */}
          <span
            aria-hidden
            className="bg-foreground size-4 shrink-0"
            style={{
              maskImage: "url(/logo.png)",
              maskSize: "contain",
              maskRepeat: "no-repeat",
              maskPosition: "center",
              WebkitMaskImage: "url(/logo.png)",
              WebkitMaskSize: "contain",
              WebkitMaskRepeat: "no-repeat",
              WebkitMaskPosition: "center",
            }}
          />
          <span className="font-semibold tracking-tight">Rota</span>
          <span className="label border-border ml-auto rounded border px-1.5 leading-4 capitalize">
            {role}
          </span>
        </Link>
        <div className="md:hidden">
          <AccountMenu username={username} compact />
        </div>
      </div>

      <nav
        aria-label="Primary"
        className="-mx-1 flex gap-0.5 overflow-x-auto px-1 md:mx-0 md:flex-col md:overflow-visible md:px-0"
      >
        {NAV.map((item) => {
          const active = item.exact
            ? pathname === item.href
            : pathname.startsWith(item.href);
          return (
            <Link
              key={item.href}
              href={item.href}
              aria-current={active ? "page" : undefined}
              className={cn(
                "flex shrink-0 items-center gap-2 rounded-md px-2 py-1.5 font-medium whitespace-nowrap transition-colors",
                "focus-visible:ring-ring focus-visible:ring-2 focus-visible:ring-offset-1 focus-visible:outline-none",
                active
                  ? "bg-sidebar-accent text-sidebar-accent-foreground"
                  : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground",
              )}
            >
              <item.icon
                className="size-3.5 shrink-0"
                strokeWidth={1.75}
                aria-hidden
              />
              {item.label}
            </Link>
          );
        })}
      </nav>

      <div className="mt-auto hidden flex-col gap-2 md:flex">
        <p className="label px-2">Times in {tz}</p>
        <AccountMenu username={username} />
      </div>
    </aside>
  );
}
