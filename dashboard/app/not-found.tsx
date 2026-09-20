import Link from "next/link"
import { AlertTriangle } from "lucide-react"
import { buttonVariants } from "@/components/ui/button"

export default function NotFound() {
  return (
    <div className="grid min-h-svh place-items-center px-6 py-16">
      <div className="w-full max-w-md">
        <AlertTriangle className="text-warning size-5" aria-hidden />
        <h1 className="mt-4 text-[1.125rem] font-semibold tracking-tight">Page not found</h1>
        <p className="text-muted-foreground mt-2">
          Nothing lives at this address. Check the link, or start again from the overview.
        </p>
        <p className="label mt-4 font-mono">HTTP 404</p>
        <Link href="/dashboard" className={`${buttonVariants({ variant: "outline" })} mt-6`}>
          Go to overview
        </Link>
      </div>
    </div>
  )
}
