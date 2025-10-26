/**
 * Format bytes to human-readable string (B, KB, MB, GB, TB)
 */
export function formatBytes(bytes: number, decimals = 2): string {
  if (bytes === 0) return "0 B"

  const k = 1024
  const dm = decimals < 0 ? 0 : decimals
  const sizes = ["B", "KB", "MB", "GB", "TB"]

  const i = Math.floor(Math.log(bytes) / Math.log(k))

  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`
}

/**
 * Format large numbers with comma separators
 */
export function formatNumber(num: number): string {
  return num.toLocaleString("en-US")
}

/**
 * Get color class based on percentage threshold
 */
export function getUsageColor(percentage: number): string {
  if (percentage >= 90) return "text-red-500"
  if (percentage >= 75) return "text-orange-500"
  if (percentage >= 60) return "text-yellow-500"
  return "text-green-500"
}

/**
 * Get progress bar variant based on percentage threshold
 */
export function getProgressVariant(percentage: number): "default" | "warning" | "destructive" {
  if (percentage >= 90) return "destructive"
  if (percentage >= 75) return "warning"
  return "default"
}
