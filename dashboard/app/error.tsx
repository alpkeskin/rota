"use client"

import { AlertTriangle } from "lucide-react"
import { Button } from "@/components/ui/button"

export default function ErrorPage({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <div className="grid min-h-svh place-items-center px-6 py-16">
      <div className="w-full max-w-md">
        <AlertTriangle className="text-warning size-5" aria-hidden />
        <h1 className="mt-4 text-[1.125rem] font-semibold tracking-tight">Something broke on this page</h1>
        <p className="text-muted-foreground mt-2">
          {error.message || "The page threw while rendering."} First check that the core API is reachable, then try again.
        </p>
        {error.digest && <p className="label mt-4 font-mono">ref {error.digest}</p>}
        <Button variant="outline" className="mt-6" onClick={reset}>
          Try again
        </Button>
      </div>
    </div>
  )
}
