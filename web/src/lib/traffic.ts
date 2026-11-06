// Small helpers for the Traffic tab, kept apart so they can be tested.

/** The server's answer times stop at this: the slowest bucket is "5 s or more". */
export const SLOWEST_BUCKET_MS = 5000

/**
 * An answer time in seconds, for people who do not think in milliseconds:
 * "0.12 s", "1.4 s". Times come from buckets, so two digits is all there is.
 */
export function formatSeconds(ms: number): string {
  if (ms >= SLOWEST_BUCKET_MS) return '5 s or more'
  if (ms < 10) return 'under 0.01 s'
  if (ms < 995) return `${(ms / 1000).toFixed(2)} s` // 0.999 would round to "1.00"
  return `${(ms / 1000).toFixed(1)} s`
}

/** Part of whole as a short percentage: "12%", "under 1%", or "0%". */
export function share(part: number, whole: number): string {
  if (!(whole > 0) || part <= 0) return '0%'
  const p = (part / whole) * 100
  if (p < 1) return 'under 1%'
  if (p > 99 && part < whole) return 'over 99%'
  return `${Math.round(p)}%`
}

export interface Change {
  /** "↑ 12%", "↓ 3%" or "0%". */
  text: string
  /** Up or down in words, for screen readers: "up 12%". */
  words: string
  tone: 'good' | 'bad' | 'same'
}

/**
 * The change from prev to cur, or null when there is nothing fair to compare
 * with (no previous period, or it was 0: any number would be "infinitely" more).
 * upIsBad turns the colours round, for errors and answer times.
 */
export function change(cur: number | null, prev: number | null | undefined, upIsBad = false): Change | null {
  if (cur === null || prev === null || prev === undefined || !(prev > 0)) return null
  const pct = Math.round(((cur - prev) / prev) * 100)
  if (pct === 0) return { text: '0%', words: 'no change', tone: 'same' }
  const n = Math.abs(pct) > 999 ? '999+' : `${Math.abs(pct)}`
  return {
    text: `${pct > 0 ? '↑' : '↓'} ${n}%`,
    words: `${pct > 0 ? 'up' : 'down'} ${n}%`,
    tone: pct > 0 !== upIsBad ? 'good' : 'bad',
  }
}

/**
 * The change of an answer time in words people use for time: "7% slower",
 * "3% faster". Slower is the bad direction.
 */
export function timeChange(cur: number | null, prev: number | null | undefined): Change | null {
  const c = change(cur, prev, true)
  if (!c || c.tone === 'same') return c
  const n = c.words.replace(/^(up|down) /, '')
  const words = `${n} ${c.words.startsWith('up') ? 'slower' : 'faster'}`
  return { text: words, words, tone: c.tone }
}

/**
 * The change of a rate (0.02 is 2%) in percentage points: from 1% to 2% is
 * "↑ 1 pt", not "↑ 100%". Up is bad: it is for error rates.
 */
export function pointChange(cur: number, prev: number | null | undefined): Change | null {
  if (prev === null || prev === undefined) return null
  const pts = Math.round((cur - prev) * 1000) / 10
  if (pts === 0) return { text: '0 pt', words: 'no change', tone: 'same' }
  const n = `${Math.abs(pts)}`
  const unit = Math.abs(pts) === 1 ? 'percentage point' : 'percentage points'
  return { text: `${pts > 0 ? '↑' : '↓'} ${n} pt`, words: `${pts > 0 ? 'up' : 'down'} ${n} ${unit}`, tone: pts > 0 ? 'bad' : 'good' }
}
