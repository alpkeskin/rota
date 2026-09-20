// Number, date and byte formatting shared by every page. Numbers stay
// tabular via the `.num` utility; these helpers only decide the text.

const nf = new Intl.NumberFormat("en-US")
const cf = new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 })

/** 1204 → "1,204" */
export function count(n: number | null | undefined): string {
  if (n == null || Number.isNaN(n)) return "—"
  return nf.format(n)
}

/** 12400 → "12.4K" — for cramped cells only. */
export function compact(n: number | null | undefined): string {
  if (n == null || Number.isNaN(n)) return "—"
  return cf.format(n)
}

/** 0–100 → "93.2%" */
export function percent(n: number | null | undefined, digits = 1): string {
  if (n == null || Number.isNaN(n)) return "—"
  return `${n.toFixed(digits)}%`
}

/** 245 → "245 ms" */
export function ms(n: number | null | undefined): string {
  if (n == null || Number.isNaN(n)) return "—"
  return `${count(Math.round(n))} ms`
}

/** Bytes → "1.2 GB" */
export function bytes(n: number | null | undefined, decimals = 1): string {
  if (n == null || Number.isNaN(n)) return "—"
  if (n === 0) return "0 B"
  const k = 1024
  const sizes = ["B", "KB", "MB", "GB", "TB"]
  const i = Math.min(Math.floor(Math.log(n) / Math.log(k)), sizes.length - 1)
  return `${parseFloat((n / Math.pow(k, i)).toFixed(decimals))} ${sizes[i]}`
}

function toDate(d: string | Date | null | undefined): Date | null {
  if (!d) return null
  const date = d instanceof Date ? d : new Date(d)
  return Number.isNaN(date.getTime()) ? null : date
}

/** "3d ago" / "in 2h" — for activity columns. */
export function relative(d: string | Date | null | undefined): string {
  const date = toDate(d)
  if (!date) return "—"
  const diff = Date.now() - date.getTime()
  const abs = Math.abs(diff)
  const suffix = diff >= 0 ? "ago" : "from now"
  const s = Math.round(abs / 1000)
  if (s < 45) return diff >= 0 ? "just now" : "in a moment"
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m ${suffix}`
  const h = Math.round(m / 60)
  if (h < 24) return `${h}h ${suffix}`
  const days = Math.round(h / 24)
  if (days < 30) return `${days}d ${suffix}`
  const months = Math.round(days / 30)
  if (months < 12) return `${months}mo ${suffix}`
  return `${Math.round(months / 12)}y ${suffix}`
}

const dateFmt = new Intl.DateTimeFormat("en-GB", { day: "2-digit", month: "short", year: "numeric" })
const dateTimeFmt = new Intl.DateTimeFormat("en-GB", {
  day: "2-digit", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit",
})
const timeFmt = new Intl.DateTimeFormat("en-GB", { hour: "2-digit", minute: "2-digit", second: "2-digit" })

/** "20 Sep 2026" — fixed moments. */
export function formatDate(d: string | Date | null | undefined): string {
  const date = toDate(d)
  return date ? dateFmt.format(date) : "—"
}

/** "20 Sep 2026, 14:03" */
export function formatDateTime(d: string | Date | null | undefined): string {
  const date = toDate(d)
  return date ? dateTimeFmt.format(date) : "—"
}

/** "14:03:21" — log lines. */
export function formatTime(d: string | Date | null | undefined): string {
  const date = toDate(d)
  return date ? timeFmt.format(date) : "—"
}

/** 90 → "1h 30m" */
export function humanizeMinutes(min: number): string {
  if (min < 60) return `${min}m`
  const h = Math.floor(min / 60)
  const m = min % 60
  if (h < 24) return m ? `${h}h ${m}m` : `${h}h`
  const d = Math.floor(h / 24)
  const rh = h % 24
  return rh ? `${d}d ${rh}h` : `${d}d`
}

/** 1500 → "1.5s" */
export function seconds(msValue: number): string {
  if (msValue < 1000) return `${Math.round(msValue)}ms`
  return `${(msValue / 1000).toFixed(msValue < 10000 ? 1 : 0)}s`
}

/** Signed delta text: 3.2 → "+3.2%", -1 → "−1.0%". */
export function signedPercent(n: number, digits = 1): string {
  if (Number.isNaN(n)) return "—"
  const sign = n > 0 ? "+" : n < 0 ? "−" : ""
  return `${sign}${Math.abs(n).toFixed(digits)}%`
}
