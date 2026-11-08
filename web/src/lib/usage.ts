// The layered bar of a usage card on Projects: what the apps use right now,
// inside what they have allocated, inside the most the person may allocate.

export interface UsageBar {
  /** Width of "in use", in percent of the bar. */
  usedPct: number
  /** Width of "allocated", in percent of the bar (at least as wide as in use). */
  allocatedPct: number
  /** Where the max sits when allocated goes past it, in percent; null otherwise. */
  maxMarkPct: number | null
  /** Allocated against the max: ok, near it (75%), at it (90%) or over it. No max is always ok. */
  level: 'ok' | 'warn' | 'full' | 'over'
}

/**
 * The bar for used, allocated and max (null or 0: no max). With a max the bar
 * is the max, or the allocation when that is bigger; without one it is the
 * allocation, so there is no empty track.
 */
export function usageBar(used: number, allocated: number, max: number | null): UsageBar {
  const hasMax = max !== null && max > 0
  const scale = Math.max(used, allocated, hasMax ? max : 0)
  if (scale <= 0) return { usedPct: 0, allocatedPct: 0, maxMarkPct: null, level: 'ok' }
  const p = (n: number) => Math.min(100, Math.max(0, (n / scale) * 100))
  const usedPct = p(used)
  const allocatedPct = Math.max(usedPct, p(allocated))
  if (!hasMax) return { usedPct, allocatedPct, maxMarkPct: null, level: 'ok' }
  const frac = allocated / max
  const level = frac > 1 ? 'over' : frac >= 0.9 ? 'full' : frac >= 0.75 ? 'warn' : 'ok'
  return { usedPct, allocatedPct, maxMarkPct: allocated > max ? p(max) : null, level }
}
