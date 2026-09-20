import * as React from "react"
import Link from "next/link"
import { ArrowLeft } from "lucide-react"
import { cn } from "@/lib/utils"

/** Every page opens with this: what the page is, how to read it, and its controls. */
export function PageHeader({
  title,
  description,
  children,
}: {
  title: React.ReactNode
  description?: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <header className="border-border flex flex-wrap items-end justify-between gap-x-6 gap-y-3 border-b px-4 py-4 md:px-6 md:py-5">
      {/* The title block grows and wraps its text; controls keep their width and only drop below on narrow screens. */}
      <div className="min-w-0 flex-1 basis-[18rem]">
        <h1 className="text-[1.0625rem] leading-tight font-semibold tracking-tight">{title}</h1>
        {description && <p className="text-muted-foreground mt-1">{description}</p>}
      </div>
      {children && <div className="flex flex-wrap items-center gap-2">{children}</div>}
    </header>
  )
}

/** A titled block. Not a card: the rule above it is the structure. */
export function Section({
  title,
  description,
  actions,
  className,
  children,
}: {
  title?: React.ReactNode
  description?: React.ReactNode
  actions?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  return (
    <section className={cn("border-border border-b px-4 py-5 md:px-6 md:py-6", className)}>
      {(title || actions) && (
        <div className="mb-4 flex flex-wrap items-end justify-between gap-x-6 gap-y-2">
          <div className="min-w-0 flex-1 basis-[18rem]">
            {title && <h2 className="font-semibold tracking-tight">{title}</h2>}
            {description && <p className="text-muted-foreground mt-0.5">{description}</p>}
          </div>
          {actions && <div className="flex items-center gap-2">{actions}</div>}
        </div>
      )}
      {children}
    </section>
  )
}

/** Plain padded block under the header (list pages). */
export function Content({ className, children }: { className?: string; children: React.ReactNode }) {
  return <div className={cn("px-4 py-4 md:px-6 md:py-5", className)}>{children}</div>
}

/** Back link + id + badges strip on detail views. */
export function SubBar({ children }: { children: React.ReactNode }) {
  return (
    <div className="border-border flex flex-wrap items-center gap-x-6 gap-y-2 border-b px-4 py-3 md:px-6">
      {children}
    </div>
  )
}

export function BackLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Link
      href={href}
      className="text-muted-foreground hover:text-foreground inline-flex items-center gap-1.5 font-medium"
    >
      <ArrowLeft className="size-3.5" aria-hidden /> {children}
    </Link>
  )
}

/** In-page sub-views: each tab is a URL. */
export function TabStrip({
  tabs,
  label,
}: {
  tabs: { href: string; label: React.ReactNode; active: boolean }[]
  label: string
}) {
  return (
    <nav className="border-border flex gap-0.5 overflow-x-auto border-b px-4 py-2 md:px-6" aria-label={label}>
      {tabs.map((t, i) => (
        <Link
          key={i}
          href={t.href}
          aria-current={t.active ? "page" : undefined}
          scroll={false}
          className={cn(
            "shrink-0 rounded-md px-2.5 py-1 font-medium whitespace-nowrap transition-colors",
            "focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
            t.active ? "bg-accent text-foreground" : "text-muted-foreground hover:text-foreground hover:bg-accent/50"
          )}
        >
          {t.label}
        </Link>
      ))}
    </nav>
  )
}

/** Secondary numbers under a section, flowing horizontally. */
export function Footnote({ items }: { items: { label: React.ReactNode; value: React.ReactNode }[] }) {
  return (
    <dl className="text-muted-foreground mt-6 flex flex-wrap gap-x-8 gap-y-2">
      {items.map((it, i) => (
        <div key={i} className="flex items-baseline gap-1.5">
          <dt>{it.label}</dt>
          <dd className="num text-foreground font-medium">{it.value}</dd>
        </div>
      ))}
    </dl>
  )
}

/** Empty state is one sentence. */
export function EmptyLine({ children, className }: { children: React.ReactNode; className?: string }) {
  return <p className={cn("text-muted-foreground py-8 text-center", className)}>{children}</p>
}

/** Loading is also one quiet line. */
export function LoadingLine({ children = "Loading…" }: { children?: React.ReactNode }) {
  return <p className="text-muted-foreground px-4 py-8 text-center md:px-6">{children}</p>
}
