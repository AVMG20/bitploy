// Made-up projects laid out the way the canvas lays them out, and the
// geometry the canvas tests check lines with.
import { laneLayout, NEAR } from '../src/lib/canvas.ts'
import type { Box, Pt } from '../src/lib/canvas.ts'

export function rng(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export interface Layout {
  cards: Box[]
  /** The add button below the last lane: no line or label may cover it. */
  extra: Box[]
  links: { from: string; to: string }[]
  w: number
  h: number
  /** Where the cards start, under the lane titles. */
  top: number
}

/**
 * The board's inner width at a window width (measured in the dashboard), its
 * padding and the gap between lanes: the canvas's (laneLayout), or a fixed
 * one for lanes narrower than the canvas would draw lines for.
 */
export const BOARDS: { name: string; w: number; pad: number; gap?: number }[] = [
  { name: '1280', w: 938, pad: 32 },
  { name: '1440', w: 1098, pad: 32 },
  { name: '1920', w: 1578, pad: 32 },
  { name: '1024 (narrow lanes)', w: 690, pad: 32, gap: 72 },
]

export function layout(seed: number, board: (typeof BOARDS)[number]): Layout {
  const r = rng(seed)
  // Cards in a lane are 12px apart, or 32px when lines pass between them (a line skips a lane).
  const cardGap = r() < 0.5 ? 12 : 32
  const count = 1 + Math.floor(r() * 12)
  const lanes: number[][] = [[], [], []]
  for (let i = 0; i < count; i++) lanes[Math.floor(r() * 3)]!.push(i)
  const used = lanes.filter((l) => l.length)
  const gap = board.gap ?? (laneLayout(board.w - 2 * board.pad, used.length).gap || 72)
  const colW = (board.w - 2 * board.pad - (used.length - 1) * gap) / used.length
  const top = board.pad + 45
  const cards: Box[] = []
  const extra: Box[] = []
  let bottom = 0
  used.forEach((ids, c) => {
    const l = board.pad + c * (colW + gap)
    let t = top
    for (const id of ids) {
      const h = r() < 0.15 ? 320 + Math.round(r() * 140) : 200 + Math.round(r() * 120)
      cards.push({ id: `a${id}`, col: c, l, r: l + colW, t, b: t + h })
      t += h + cardGap
    }
    if (c === used.length - 1) {
      extra.push({ id: 'add', col: c, l, r: l + colW, t, b: t + 62 })
      t += 62 + cardGap
    }
    bottom = Math.max(bottom, t - cardGap)
  })
  // Each app reads up to four others.
  const density = 0.05 + r() * 0.3
  const links: Layout['links'] = []
  for (const a of cards) {
    let reads = 0
    for (const b of cards) {
      if (a !== b && reads < 4 && r() < density) {
        links.push({ from: a.id, to: b.id })
        reads++
      }
    }
  }
  return { cards, extra, links, w: board.w, h: bottom + board.pad, top }
}

// ---------------------------------------------------------------- geometry of routed lines

export interface Seg {
  a: Pt
  b: Pt
}
/** The runs of a line; two runs one after the other on one level (or upright) are one run, so a crossing at the point between them counts. */
export const segs = (pts: readonly Pt[]): Seg[] => {
  const out: Seg[] = []
  for (const s of pts.slice(1).map((b, i) => ({ a: pts[i]!, b }))) {
    if (Math.abs(s.a.x - s.b.x) + Math.abs(s.a.y - s.b.y) <= 0.01) continue
    const last = out.at(-1)
    const lvl = (x: Seg) => Math.abs(x.a.y - x.b.y) < 0.01
    if (last && lvl(last) === lvl(s) && (lvl(s) ? Math.abs(last.a.y - s.a.y) < 0.01 : Math.abs(last.a.x - s.a.x) < 0.01)) out[out.length - 1] = { a: last.a, b: s.b }
    else out.push(s)
  }
  return out
}
const level = (s: Seg) => Math.abs(s.a.y - s.b.y) < 0.01
const span = (u: number, v: number): [number, number] => (u < v ? [u, v] : [v, u])

/** Points where a level run of one line crosses an upright run of the other. */
export function crossings(p: readonly Pt[], q: readonly Pt[]): number {
  let n = 0
  for (const s of segs(p)) {
    for (const t of segs(q)) {
      if (level(s) === level(t)) continue
      const [h, v] = level(s) ? [s, t] : [t, s]
      const [x1, x2] = span(h.a.x, h.b.x)
      const [y1, y2] = span(v.a.y, v.b.y)
      if (v.a.x > x1 + 0.5 && v.a.x < x2 - 0.5 && h.a.y > y1 + 0.5 && h.a.y < y2 - 0.5) n++
    }
  }
  return n
}

/** Parallel runs of two lines closer than NEAR along a stretch. */
export function overlaps(p: readonly Pt[], q: readonly Pt[]): number {
  let n = 0
  for (const s of segs(p)) {
    for (const t of segs(q)) {
      if (level(s) !== level(t)) continue
      const [k, along] = level(s) ? (['y', 'x'] as const) : (['x', 'y'] as const)
      if (Math.abs(s.a[k] - t.a[k]) >= NEAR) continue
      const [s1, s2] = span(s.a[along], s.b[along])
      const [t1, t2] = span(t.a[along], t.b[along])
      if (Math.min(s2, t2) - Math.max(s1, t1) > 0.5) n++
    }
  }
  return n
}

