// The lines on the project canvas: from each app to the apps whose variables
// it reads. Cards sit in columns (the lanes). Every line is made of level runs
// and upright runs; the upright runs sit in the gaps between columns (or in the
// margin beside the first and last column), so a line never enters a card.
//
// How a line goes:
// - Between neighbouring columns: out of the facing sides, one upright run in
//   the gap between them.
// - Within one column: out of the same side of both cards, one upright run in
//   the gap on that side (the margin for the first column, else the right).
// - Past a column: level through the free room below that column's cards when
//   one end is low enough, else down into the gap, below the column, and up
//   again in the next gap.
//
// Then the router looks for the fewest crossings. Each card side has its
// ports (where lines meet it) in some order, each gap its upright runs in some
// order left to right. Given the ports, the best order in a gap is found
// exactly (up to 8 runs) or by swapping and moving runs until nothing helps.
// The ports, the way past a column and the side of a line within one column
// are then improved by swaps while a swap helps. What counts, worst first:
// two lines on top of each other; two lines between neighbouring columns
// crossing though their cards do not force it, or two lines crossing twice;
// any other crossing.

export interface Pt {
  x: number
  y: number
}

/** A card on the board, in board coordinates, and the column (lane) it is in. Boxes no link names are kept clear too. */
export interface Box {
  id: string
  col: number
  l: number
  r: number
  t: number
  b: number
}

export interface RoutedLink {
  key: string
  from: string
  to: string
  points: Pt[]
  /** SVG path data with rounded corners. */
  d: string
  /** Where the line enters the card it reads from, and on which side. */
  end: Pt
  endSide: Side
}

export interface RouteOptions {
  /** The board's width; the margin right of the last column ends there. */
  width?: number
  radius?: number
  /** Stop looking for fewer crossings after this long (ms). */
  budget?: number
}

export type Side = 'l' | 'r'

/** How far below a card's top the first port is (under the name). */
const PORT_TOP = 34
/** The lowest port stays this far above the card's bottom. */
const PORT_BOTTOM = 14
/** How far a port moves off a port of another line facing it. */
const NUDGE = 5
/** The most room between two ports on one side. */
const PORT_STEP = 22
/** Room kept between a card and a level run passing below it. */
const CLEAR = 10
/** The first run below a column is this far under its lowest card. */
const BELOW = 18
const BELOW_STEP = 8
/** The most and the least room between two upright runs in one gap. */
const TRACK_STEP = 10
const MIN_STEP = 3
/** Two upright runs at least this far apart, one above the other, may share a place. */
const SHARE = 8
/** Room kept between the upright runs and the cards on both sides of a gap. */
const gapMargin = (w: number) => Math.min(10, w / 5)
/** Two parallel runs closer than this read as one line. */
export const NEAR = 2
const EPS = 0.5
/** What two lines running on top of each other cost, against 1 for a crossing. */
const OVERLAP = 1000
/** What a crossing costs between two lines of neighbouring columns whose cards do not force it. */
const FREE = 40
/** Gaps with at most this many upright runs are ordered exactly. */
const EXACT = 8

type Way = 'adjacent' | 'same-l' | 'same-r' | 'start' | 'end' | 'below'

interface End {
  /** The line, by its place in the plans. */
  plan: number
  /**
   * A line between neighbouring columns: its ends keep the order of the cards
   * at their other ends, else two lines would cross that need not.
   */
  fixed: boolean
  box: Box
  side: Side
  /** Initial order on the side: the middle of the other card. */
  key: number
  y: number
}

interface Arm {
  y: number
  /** Which way the level run leaves the upright one. */
  dir: Side
}

/** An upright run in a gap and the level runs on its ends. */
interface Part {
  id: string
  gap: number
  arms: [Arm, Arm]
  lo: number
  hi: number
  /** Arms only on one side: a line between two cards of one column. */
  bracket?: Side
  /** A line between neighbouring columns: its card on the left and on the right. */
  cards?: [Box, Box]
  x: number
}

/** A level run crossing a whole gap. */
interface Pass {
  link: number
  gap: number
  y: number
}

interface Plan {
  i: number
  a: Box
  b: Box
  ca: number
  cb: number
  ea: End
  eb: End
  way: Way
  ways: Way[]
  /** The run below the columns in between. */
  belowY: number
}

const inside = (y: number, p: Pick<Part, 'lo' | 'hi'>) => y > p.lo + EPS && y < p.hi - EPS
const near = (a: number, b: number) => Math.abs(a - b) < NEAR

/**
 * Whether two lines between the same neighbouring columns must cross: one
 * line's card is above the other's in one column and below it in the other.
 */
export function interleave(x: readonly [Box, Box], y: readonly [Box, Box]): boolean {
  return x[0] !== y[0] && x[1] !== y[1] && Math.sign(x[0].t - y[0].t) !== Math.sign(x[1].t - y[1].t)
}

/**
 * Route every link between the boxes. Links naming a box that is not there,
 * and a second link between the same two apps the same way, are left out.
 */
export function routeLinks(boxes: readonly Box[], links: readonly { from: string; to: string }[], opts: RouteOptions = {}): RoutedLink[] {
  const radius = opts.radius ?? 8
  const byId = new Map(boxes.map((b) => [b.id, b]))
  // Columns by position: a missing lane leaves no empty column.
  const colKeys = [...new Set(boxes.map((b) => b.col))].sort((x, y) => x - y)
  const colOf = new Map(colKeys.map((c, i) => [c, i]))
  const n = colKeys.length
  const inCol = colKeys.map((c) => boxes.filter((b) => b.col === c))
  const L = inCol.map((bs) => Math.min(...bs.map((b) => b.l)))
  const R = inCol.map((bs) => Math.max(...bs.map((b) => b.r)))
  const width = opts.width ?? (n ? R[n - 1]! + 32 : 0)
  /** Gap g lies left of column g; gap n is the margin right of the last column. */
  const gapL = (g: number) => (g === 0 ? 0 : R[g - 1]!)
  const gapR = (g: number) => (g === n ? width : L[g]!)

  /** Whether a level run at y passes columns lo..hi (inclusive) clear of their cards. */
  const clear = (y: number, lo: number, hi: number) => {
    for (let c = lo; c <= hi; c++) {
      const bs = inCol[c]!
      if (y <= Math.min(...bs.map((b) => b.t))) return false
      if (bs.some((b) => y > b.t - CLEAR && y < b.b + CLEAR)) return false
    }
    return true
  }
  const bottomOf = (lo: number, hi: number) => Math.max(...inCol.slice(lo, hi + 1).flatMap((bs) => bs.map((b) => b.b)))

  // ---------------------------------------------------------------- the plans
  const plans: Plan[] = []
  const seen = new Set<string>()
  links.forEach((l, i) => {
    const a = byId.get(l.from)
    const b = byId.get(l.to)
    const key = `${l.from}>${l.to}`
    if (!a || !b || a === b || seen.has(key)) return
    seen.add(key)
    const ca = colOf.get(a.col)!
    const cb = colOf.get(b.col)!
    let ways: Way[]
    if (ca === cb) ways = ca === 0 ? ['same-l', 'same-r'] : ['same-r', 'same-l']
    else if (Math.abs(ca - cb) === 1) ways = ['adjacent']
    else ways = ['start', 'end', 'below']
    const sa: Side = ways[0] === 'same-l' ? 'l' : ca === cb ? 'r' : ca < cb ? 'r' : 'l'
    const sb: Side = ca === cb ? sa : ca < cb ? 'l' : 'r'
    plans.push({
      i,
      a,
      b,
      ca,
      cb,
      ea: { plan: plans.length, fixed: ways[0] === 'adjacent', box: a, side: sa, key: (b.t + b.b) / 2, y: 0 },
      eb: { plan: plans.length, fixed: ways[0] === 'adjacent', box: b, side: sb, key: (a.t + a.b) / 2, y: 0 },
      way: ways[0]!,
      ways,
      belowY: 0,
    })
  })
  if (!plans.length) return []

  // ---------------------------------------------------------------- ports and runs below
  const sides = new Map<string, End[]>()
  const sideKey = (e: End) => `${e.box.id}:${e.side}`
  const addEnd = (e: End) => {
    const list = sides.get(sideKey(e)) ?? []
    let k = list.findIndex((x) => x.key > e.key)
    if (k < 0) k = list.length
    list.splice(k, 0, e)
    sides.set(sideKey(e), list)
  }
  const dropEnd = (e: End) => {
    const list = sides.get(sideKey(e))!
    list.splice(list.indexOf(e), 1)
  }
  for (const p of plans) {
    addEnd(p.ea)
    addEnd(p.eb)
  }
  const placePorts = () => {
    for (const ends of sides.values()) {
      if (!ends.length) continue
      const box = ends[0]!.box
      let top = box.t + PORT_TOP
      let bottom = box.b - PORT_BOTTOM
      if (bottom < top) top = bottom = (box.t + box.b) / 2
      const step = ends.length > 1 ? Math.min(PORT_STEP, (bottom - top) / (ends.length - 1)) : 0
      ends.forEach((e, k) => (e.y = top + k * step))
    }
    // A port facing a port of another line across a gap at the same height
    // moves a little, else the two lines could only run on top of each other.
    for (const [key, ends] of sides) {
      if (!ends.length || !key.endsWith(':l')) continue
      const box = ends[0]!.box
      const col = colOf.get(box.col)!
      if (col === 0) continue
      const facing = [...sides.values()].flatMap((es) => (es.length && es[0]!.side === 'r' && colOf.get(es[0]!.box.col) === col - 1 ? es : []))
      const taken = (y: number, e: End) => facing.some((f) => f.plan !== e.plan && near(f.y, y)) || ends.some((f) => f !== e && near(f.y, y))
      for (const e of ends) {
        if (!facing.some((f) => f.plan !== e.plan && near(f.y, e.y))) continue
        const y = [e.y + NUDGE, e.y - NUDGE, e.y + 2 * NUDGE, e.y - 2 * NUDGE].find((v) => v > box.t + 12 && v < box.b - 8 && !taken(v, e))
        if (y !== undefined) e.y = y
      }
    }
  }
  /** The runs below the columns lo..hi, top to bottom. */
  const belows = new Map<string, Plan[]>()
  const belowKey = (p: Plan) => `${Math.min(p.ca, p.cb) + 1}-${Math.max(p.ca, p.cb) - 1}`
  const placeBelow = () => {
    for (const [k, list] of belows) {
      const [lo, hi] = k.split('-').map(Number) as [number, number]
      const base = bottomOf(lo, hi) + BELOW
      list.forEach((p, j) => (p.belowY = base + j * BELOW_STEP))
    }
  }
  const setWay = (p: Plan, way: Way) => {
    if (p.way === way) return
    if (p.way === 'below') belows.get(belowKey(p))!.splice(belows.get(belowKey(p))!.indexOf(p), 1)
    if (way === 'below') {
      // Lines whose ends are lower run lower, so two of them need not cross.
      const list = belows.get(belowKey(p)) ?? []
      const low = (x: Plan) => x.ea.y + x.eb.y
      let k = list.findIndex((x) => low(x) > low(p))
      if (k < 0) k = list.length
      list.splice(k, 0, p)
      belows.set(belowKey(p), list)
    }
    if (p.way.startsWith('same') !== way.startsWith('same')) throw new Error('a link cannot change its kind')
    if (p.way.startsWith('same') && way !== p.way) {
      dropEnd(p.ea)
      dropEnd(p.eb)
      p.ea.side = p.eb.side = way === 'same-l' ? 'l' : 'r'
      addEnd(p.ea)
      addEnd(p.eb)
    }
    p.way = way
  }

  // ---------------------------------------------------------------- the runs of each line
  /** The same line the same way at the same heights gives the same parts, so their costs are kept. */
  const partsMemo = new Map<string, { parts: Part[]; passes: Pass[] } | null>()
  const partsOf = (p: Plan): { parts: Part[]; passes: Pass[] } | null => {
    const key = `${p.i}|${p.way}|${p.ea.y}|${p.eb.y}|${p.way === 'below' ? p.belowY : ''}`
    if (!partsMemo.has(key)) partsMemo.set(key, makeParts(p))
    return partsMemo.get(key)!
  }
  const makeParts = (p: Plan): { parts: Part[]; passes: Pass[] } | null => {
    const { ca, cb, ea, eb, i } = p
    const ya = ea.y
    const yb = eb.y
    const part = (k: number, gap: number, y1: number, d1: Side, y2: number, d2: Side, bracket?: Side): Part => ({
      id: `${i}:${k}`,
      gap,
      arms: [
        { y: y1, dir: d1 },
        { y: y2, dir: d2 },
      ],
      lo: Math.min(y1, y2),
      hi: Math.max(y1, y2),
      bracket,
      x: 0,
    })
    const passes = (from: number, to: number, y: number): Pass[] => {
      const out: Pass[] = []
      for (let g = from; g <= to; g++) out.push({ link: i, gap: g, y })
      return out
    }
    if (p.way === 'same-r') return { parts: [part(0, ca + 1, ya, 'l', yb, 'l', 'l')], passes: [] }
    if (p.way === 'same-l') return { parts: [part(0, ca, ya, 'r', yb, 'r', 'r')], passes: [] }
    const right = ca < cb
    const dirA: Side = right ? 'l' : 'r'
    const dirB: Side = right ? 'r' : 'l'
    if (p.way === 'adjacent') return { parts: [{ ...part(0, Math.max(ca, cb), ya, dirA, yb, dirB), cards: right ? [p.a, p.b] : [p.b, p.a] }], passes: [] }
    // Past the columns lo..hi.
    const lo = Math.min(ca, cb) + 1
    const hi = Math.max(ca, cb) - 1
    const gA = right ? ca + 1 : ca
    const gB = right ? cb : cb + 1
    if (p.way === 'start') {
      if (!clear(ya, lo, hi)) return null
      return { parts: [part(0, gB, ya, dirA, yb, dirB)], passes: right ? passes(gA, gB - 1, ya) : passes(gB + 1, gA, ya) }
    }
    if (p.way === 'end') {
      if (!clear(yb, lo, hi)) return null
      return { parts: [part(0, gA, ya, dirA, yb, dirB)], passes: right ? passes(gA + 1, gB, yb) : passes(gB, gA - 1, yb) }
    }
    const y = p.belowY
    return {
      parts: [part(0, gA, ya, dirA, y, dirB), part(1, gB, y, dirA, yb, dirB)],
      passes: right ? passes(gA + 1, gB - 1, y) : passes(gB + 1, gA - 1, y),
    }
  }

  // ---------------------------------------------------------------- ordering the runs in a gap
  /** What it costs when part x is left of part y. */
  const pairCost = (x: Part, y: Part) => {
    let k = 0
    let c = 0
    for (const a of x.arms) {
      if (a.dir !== 'r') continue
      if (inside(a.y, y)) k++
      for (const b of y.arms) if (b.dir === 'l' && near(a.y, b.y)) c += OVERLAP
    }
    for (const b of y.arms) if (b.dir === 'l' && inside(b.y, x)) k++
    // Two lines crossing twice is worse than many crossings elsewhere.
    if (k === 2) k = FREE
    c += x.cards && y.cards && !interleave(x.cards, y.cards) ? k * FREE : k
    // A line between two cards of one column keeps to that column.
    if (x.bracket === 'r') c += 1e-4
    if (y.bracket === 'l') c += 1e-4
    return c
  }
  const orderCost = (order: readonly number[], C: readonly Float64Array[]) => {
    let s = 0
    for (let i = 0; i < order.length; i++) for (let j = i + 1; j < order.length; j++) s += C[order[i]!]![order[j]!]!
    return s
  }
  /** The order of the parts left to right with the fewest crossings, and its cost. */
  const orderGap = (parts: readonly Part[], warm: readonly string[] | undefined, crowd: (order: readonly number[]) => number): { cost: number; order: number[] } => {
    const m = parts.length
    const C = parts.map((x) => Float64Array.from(parts, (y) => (x === y ? 0 : pairCost(x, y))))
    if (m <= 1) return { cost: 0, order: m ? [0] : [] }
    // The order found, or the last one if that costs less with the room it needs.
    const pick = (found: { cost: number; order: number[] }) => {
      let best = { cost: found.cost + crowd(found.order), order: found.order }
      const pos = new Map(parts.map((x, k) => [x.id, k]))
      const last = warm?.map((id) => pos.get(id))
      if (last?.length === m && last.every((k) => k !== undefined)) {
        const c = orderCost(last, C) + crowd(last)
        if (c < best.cost - 1e-9) best = { cost: c, order: last }
      }
      return best
    }
    if (m <= EXACT) {
      // best[mask]: the cheapest way to put the parts in mask leftmost.
      const full = (1 << m) - 1
      const best = new Float64Array(full + 1).fill(Infinity)
      const last = new Int8Array(full + 1).fill(-1)
      // into[mask * m + j]: what j costs right of every part in mask.
      const into = new Float64Array((full + 1) * m)
      best[0] = 0
      for (let mask = 1; mask <= full; mask++) {
        const low = 31 - Math.clz32(mask & -mask)
        const rest = mask & (mask - 1)
        for (let j = 0; j < m; j++) into[mask * m + j] = into[rest * m + j]! + C[low]![j]!
      }
      for (let mask = 0; mask < full; mask++) {
        const b = best[mask]!
        if (b === Infinity) continue
        for (let j = 0; j < m; j++) {
          if (mask & (1 << j)) continue
          const next = mask | (1 << j)
          const c = b + into[mask * m + j]!
          if (c < best[next]! - 1e-9) {
            best[next] = c
            last[next] = j
          }
        }
      }
      const order: number[] = []
      for (let mask = full; mask; mask &= ~(1 << last[mask]!)) order.unshift(last[mask]!)
      return pick({ cost: best[full]!, order })
    }
    // Crowded: lines between neighbouring columns first, in an order where
    // none of them cross that need not (one going up runs left of a lower one
    // going up; one going down runs right of a lower one going down). Then each
    // other part where it costs least. Or the last order, if that costs less.
    const adj = parts.map((_, k) => k).filter((k) => parts[k]!.cards)
    const up = (x: Part) => (x.arms[0].dir === 'l' ? x.arms[1].y - x.arms[0].y : x.arms[0].y - x.arms[1].y) <= 0
    const leftY = (x: Part) => (x.arms[0].dir === 'l' ? x.arms[0].y : x.arms[1].y)
    const ups = adj.filter((k) => up(parts[k]!)).sort((x, y) => leftY(parts[x]!) - leftY(parts[y]!))
    const downs = adj.filter((k) => !up(parts[k]!)).sort((x, y) => leftY(parts[y]!) - leftY(parts[x]!))
    let order = [...ups, ...downs]
    for (let k = 0; k < m; k++) {
      if (parts[k]!.cards) continue
      let best = Infinity
      let at = 0
      for (let j = 0; j <= order.length; j++) {
        let c = 0
        for (let q = 0; q < order.length; q++) c += q < j ? C[order[q]!]![k]! : C[k]![order[q]!]!
        if (c < best) [best, at] = [c, j]
      }
      order.splice(at, 0, k)
    }
    if (warm) {
      const pos = new Map(warm.map((id, k) => [id, k]))
      const last = parts.map((_, k) => k).sort((x, y) => (pos.get(parts[x]!.id) ?? m + parts[x]!.lo) - (pos.get(parts[y]!.id) ?? m + parts[y]!.lo))
      if (orderCost(last, C) < orderCost(order, C)) order = last
    }
    let cost = orderCost(order, C)
    for (let round = 0; round < 50; round++) {
      let better = false
      // Swap two parts: what changes is their own pair and each part between them.
      for (let i = 0; i < m; i++) {
        for (let j = i + 1; j < m; j++) {
          const x = order[i]!
          const y = order[j]!
          let d = C[y]![x]! - C[x]![y]!
          for (let k = i + 1; k < j; k++) {
            const z = order[k]!
            d += C[y]![z]! + C[z]![x]! - C[x]![z]! - C[z]![y]!
          }
          if (d < -1e-9) {
            order[i] = y
            order[j] = x
            cost += d
            better = true
          }
        }
      }
      // Move one part elsewhere.
      for (let i = 0; i < m; i++) {
        const x = order[i]!
        let bestD = -1e-9
        let bestJ = -1
        let d = 0
        for (let j = i - 1; j >= 0; j--) {
          const z = order[j]!
          d += C[x]![z]! - C[z]![x]!
          if (d < bestD) [bestD, bestJ] = [d, j]
        }
        d = 0
        for (let j = i + 1; j < m; j++) {
          const z = order[j]!
          d += C[z]![x]! - C[x]![z]!
          if (d < bestD) [bestD, bestJ] = [d, j]
        }
        if (bestJ >= 0) {
          order.splice(bestJ, 0, order.splice(i, 1)[0]!)
          cost += bestD
          better = true
        }
      }
      if (!better) break
    }
    return pick({ cost, order })
  }

  /**
   * Where the parts sit left to right, in steps: a part shares the place of
   * the one before it when their upright runs are far apart (one above the
   * other), else it goes a step further right than every part it is near.
   */
  const slots = (inOrder: readonly Part[]): number[] => {
    const at: number[] = []
    inOrder.forEach((x, k) => {
      let s = k ? at[k - 1]! : 0
      for (let j = 0; j < k; j++) {
        const y = inOrder[j]!
        if (x.lo < y.hi + SHARE && y.lo < x.hi + SHARE) s = Math.max(s, at[j]! + 1)
      }
      at.push(s)
    })
    return at
  }

  // ---------------------------------------------------------------- the cost of a plan
  const orders = new Map<number, string[]>()
  const cache = new Map<string, { cost: number; order: number[] }>()
  let built: { parts: Part[]; passes: Pass[] }[] = []
  const evaluate = (): number => {
    placePorts()
    placeBelow()
    const all: { parts: Part[]; passes: Pass[] }[] = []
    let cost = 0
    for (const p of plans) {
      const r = partsOf(p)
      if (!r) return Infinity
      all.push(r)
      if (p.way === 'below') cost += 0.5
      if (p.way !== p.ways[0] && p.way.startsWith('same')) cost += 0.25
    }
    const partsIn: Part[][] = Array.from({ length: n + 1 }, () => [])
    const passesIn: Pass[][] = Array.from({ length: n + 1 }, () => [])
    for (const r of all) {
      for (const x of r.parts) partsIn[x.gap]!.push(x)
      for (const x of r.passes) passesIn[x.gap]!.push(x)
    }
    for (let g = 0; g <= n; g++) {
      const parts = partsIn[g]!
      const passes = passesIn[g]!
      let sig = `${g}`
      for (const x of parts) sig += ` ${x.id}/${Math.round(x.arms[0].y * 100)}${x.arms[0].dir}/${Math.round(x.arms[1].y * 100)}${x.arms[1].dir}`
      sig += ' |'
      for (const x of passes) sig += ` ${x.link}/${Math.round(x.y * 100)}`
      // The last order is tried too, so it is part of what was asked.
      sig += ` | ${orders.get(g)?.join(' ') ?? ''}`
      let res = cache.get(sig)
      if (!res) {
        // What does not depend on the order: level runs across the whole gap, and arms the same way at one height.
        let fixed = 0
        for (const s of passes) {
          for (const x of parts) {
            if (x.id.startsWith(`${s.link}:`)) continue
            if (inside(s.y, x)) fixed++
            if (near(x.arms[0].y, s.y) || near(x.arms[1].y, s.y)) fixed += OVERLAP
          }
          for (const t of passes) if (t.link < s.link && near(t.y, s.y)) fixed += OVERLAP
        }
        for (let i = 0; i < parts.length; i++) {
          for (let j = i + 1; j < parts.length; j++) {
            for (const a of parts[i]!.arms) for (const b of parts[j]!.arms) if (a.dir === b.dir && near(a.y, b.y)) fixed += OVERLAP
          }
        }
        // A gap holds as many upright runs side by side as fit MIN_STEP apart.
        const w = gapR(g) - gapL(g)
        const room = Math.floor((w - 2 * gapMargin(w)) / MIN_STEP)
        const crowd = (order: readonly number[]) => slots(order.map((k) => parts[k]!)).reduce((c, at) => c + Math.max(0, at - room) * OVERLAP, 0)
        const o = orderGap(parts, orders.get(g), crowd)
        res = { cost: o.cost + fixed, order: o.order }
        cache.set(sig, res)
      }
      orders.set(
        g,
        res.order.map((k) => parts[k]!.id),
      )
      cost += res.cost
    }
    built = all
    return cost
  }

  // ---------------------------------------------------------------- a first plan, then swaps while they help
  placePorts()
  for (let round = 0; round < 3; round++) {
    let changed = false
    for (const p of plans) {
      if (p.ways[0] !== 'start') continue
      const lo = Math.min(p.ca, p.cb) + 1
      const hi = Math.max(p.ca, p.cb) - 1
      const way: Way = clear(p.ea.y, lo, hi) ? 'start' : clear(p.eb.y, lo, hi) ? 'end' : 'below'
      if (way !== p.way) {
        setWay(p, way)
        changed = true
      }
      // A line going below the columns leaves and enters low.
      if (way === 'below' && p.ea.key < 1e6) {
        p.ea.key = p.eb.key = 1e6 + p.i
        dropEnd(p.ea)
        dropEnd(p.eb)
        addEnd(p.ea)
        addEnd(p.eb)
        changed = true
      }
    }
    placePorts()
    if (!changed) break
  }
  for (const list of belows.values()) list.sort((x, y) => x.ea.y + x.eb.y - (y.ea.y + y.eb.y))

  // ---------------------------------------------------------------- where the upright runs go
  const placeTracks = () => {
    for (let g = 0; g <= n; g++) {
      const ids = orders.get(g) ?? []
      if (!ids.length) continue
      const l = gapL(g)
      const w = gapR(g) - l
      const byId = new Map(built.flatMap((line) => line.parts.filter((x) => x.gap === g).map((x) => [x.id, x] as const)))
      const inOrder = ids.map((id) => byId.get(id)!)
      const at = slots(inOrder)
      const span = at.at(-1)!
      const step = span ? Math.min(TRACK_STEP, (w - 2 * gapMargin(w)) / span) : 0
      inOrder.forEach((x, k) => (x.x = l + w / 2 + (at[k]! - span / 2) * step))
    }
  }
  const pointsOf = (k: number): Pt[] => {
    const p = plans[k]!
    const points: Pt[] = [{ x: p.ea.side === 'r' ? p.a.r : p.a.l, y: p.ea.y }]
    for (const x of built[k]!.parts) points.push({ x: x.x, y: x.arms[0].y }, { x: x.x, y: x.arms[1].y })
    points.push({ x: p.eb.side === 'r' ? p.b.r : p.b.l, y: p.eb.y })
    return points
  }
  function involved(): Set<number> {
    placeTracks()
    const lines = plans.map((_, k) => pointsOf(k))
    const out = new Set<number>()
    for (let i = 0; i < lines.length; i++) {
      for (let j = i + 1; j < lines.length; j++) {
        if (crossingCount(lines[i]!, lines[j]!)) out.add(i).add(j)
      }
    }
    return out
  }

  let cost = evaluate()
  if (cost === Infinity) {
    for (const p of plans) if (p.ways[0] === 'start') setWay(p, 'below')
    cost = evaluate()
  }
  /** The lines that cross another line now: a port swap helps only if one of its lines is among them. */
  let crossed = involved()
  const tryMove = (apply: () => void, undo: () => void) => {
    const kept = new Map(orders)
    const keptBuilt = built
    apply()
    const c = evaluate()
    if (c < cost - 1e-9) {
      cost = c
      crossed = involved()
      return true
    }
    undo()
    orders.clear()
    for (const [g, o] of kept) orders.set(g, o)
    built = keptBuilt
    return false
  }
  const until = performance.now() + (opts.budget ?? Infinity)
  for (let round = 0; round < 30 && cost >= 1 && performance.now() < until; round++) {
    let better = false
    for (const ends of sides.values()) {
      for (let i = 0; i < ends.length; i++) {
        for (let j = i + 1; j < ends.length; j++) {
          if (!crossed.has(ends[i]!.plan) && !crossed.has(ends[j]!.plan)) continue
          // Fixed ends keep their order among each other.
          if (ends[i]!.fixed || ends[j]!.fixed) {
            if (ends[i]!.fixed && ends[j]!.fixed) continue
            if (ends.slice(i + 1, j).some((e) => e.fixed)) continue
          }
          const swap = () => ([ends[i], ends[j]] = [ends[j]!, ends[i]!])
          if (tryMove(swap, swap)) better = true
        }
      }
    }
    for (const p of plans) {
      for (const w of p.ways) {
        if (w === p.way) continue
        const was = p.way
        const snapshot = new Map([...sides].map(([k, v]) => [k, [...v]]))
        const belowSnap = new Map([...belows].map(([k, v]) => [k, [...v]]))
        const undo = () => {
          p.way = was
          if (was.startsWith('same')) p.ea.side = p.eb.side = was === 'same-l' ? 'l' : 'r'
          sides.clear()
          for (const [k, v] of snapshot) sides.set(k, v)
          belows.clear()
          for (const [k, v] of belowSnap) belows.set(k, v)
        }
        if (tryMove(() => setWay(p, w), undo)) {
          better = true
          break
        }
      }
    }
    for (const list of belows.values()) {
      for (let i = 0; i < list.length; i++) {
        for (let j = i + 1; j < list.length; j++) {
          const swap = () => ([list[i], list[j]] = [list[j]!, list[i]!])
          if (tryMove(swap, swap)) better = true
        }
      }
    }
    if (!better) break
  }
  evaluate()
  placeTracks()

  return plans.map((p, k) => {
    const points = pointsOf(k)
    const end = points.at(-1)!
    const l = links[p.i]!
    return { key: `${l.from}>${l.to}`, from: l.from, to: l.to, points, d: roundedPath(points, radius), end, endSide: p.eb.side }
  })
}

/** How often a level run of one line crosses an upright run of the other. */
export function crossingCount(p: readonly Pt[], q: readonly Pt[]): number {
  let n = 0
  for (let i = 0; i + 1 < p.length; i++) {
    for (let j = 0; j + 1 < q.length; j++) {
      const a = p[i]!
      const b = p[i + 1]!
      const c = q[j]!
      const d = q[j + 1]!
      let h: [Pt, Pt]
      let v: [Pt, Pt]
      if (Math.abs(a.y - b.y) < 0.01 && Math.abs(c.x - d.x) < 0.01) [h, v] = [[a, b], [c, d]]
      else if (Math.abs(c.y - d.y) < 0.01 && Math.abs(a.x - b.x) < 0.01) [h, v] = [[c, d], [a, b]]
      else continue
      const x = v[0].x
      const y = h[0].y
      if (x > Math.min(h[0].x, h[1].x) + EPS && x < Math.max(h[0].x, h[1].x) - EPS && y > Math.min(v[0].y, v[1].y) + EPS && y < Math.max(v[0].y, v[1].y) - EPS) n++
    }
  }
  return n
}

/** The lowest point of the lines: the board grows to keep it inside. */
export function routesBottom(routes: readonly RoutedLink[]): number {
  let y = 0
  for (const r of routes) for (const p of r.points) y = Math.max(y, p.y)
  return y
}

/** An SVG path through the points with rounded corners. */
export function roundedPath(points: readonly Pt[], radius = 8): string {
  // Drop repeated points and points on a straight run.
  const pts: Pt[] = []
  for (const p of points) {
    const last = pts.at(-1)
    if (last && Math.abs(last.x - p.x) < 0.5 && Math.abs(last.y - p.y) < 0.5) continue
    const prev = pts.at(-2)
    if (prev && last && ((Math.abs(prev.x - last.x) < 0.5 && Math.abs(last.x - p.x) < 0.5) || (Math.abs(prev.y - last.y) < 0.5 && Math.abs(last.y - p.y) < 0.5))) pts.pop()
    pts.push(p)
  }
  if (!pts.length) return ''
  const f = (n: number) => String(Math.round(n * 10) / 10)
  let d = `M${f(pts[0]!.x)},${f(pts[0]!.y)}`
  for (let i = 1; i < pts.length - 1; i++) {
    const a = pts[i - 1]!
    const c = pts[i]!
    const b = pts[i + 1]!
    const la = Math.hypot(c.x - a.x, c.y - a.y)
    const lb = Math.hypot(b.x - c.x, b.y - c.y)
    const r = Math.min(radius, la / 2, lb / 2)
    const p1 = { x: c.x + ((a.x - c.x) / la) * r, y: c.y + ((a.y - c.y) / la) * r }
    const p2 = { x: c.x + ((b.x - c.x) / lb) * r, y: c.y + ((b.y - c.y) / lb) * r }
    d += ` L${f(p1.x)},${f(p1.y)} Q${f(c.x)},${f(c.y)} ${f(p2.x)},${f(p2.y)}`
  }
  const z = pts.at(-1)!
  return `${d} L${f(z.x)},${f(z.y)}`
}

// ---------------------------------------------------------------- labels

/** Where a label wants to be: it reads away from (x, y) toward `side`, `width` wide at most. */
export interface LabelSpot {
  key: string
  x: number
  y: number
  side: Side
  width: number
}

export interface PlacedLabel {
  key: string
  left: number
  top: number
  width: number
  height: number
}

type Rect = Pick<Box, 'l' | 'r' | 't' | 'b'>

/** How wide a label at height top..top+height reading from x toward side may be before it meets a rect or the edge. */
function roomAt(x: number, top: number, height: number, side: Side, rects: readonly Rect[], width: number): number {
  const gap = 4
  let edge = side === 'l' ? 2 : width - 2
  for (const b of rects) {
    if (b.t >= top + height + 2 || b.b <= top - 2) continue
    if (b.l < x - EPS && b.r > x + EPS) return 0
    if (side === 'l' && b.r <= x + EPS) edge = Math.max(edge, b.r + gap)
    if (side === 'r' && b.l >= x - EPS) edge = Math.min(edge, b.l - gap)
  }
  return Math.max(0, side === 'l' ? x - edge : edge - x)
}

/**
 * Place labels next to their spots without covering a box or another label
 * and inside the board: a label moves up or down a row when its spot is
 * taken, is cut short where a box starts, and is left out when no row near
 * its spot has `min` pixels of room.
 */
export function placeLabels(spots: readonly LabelSpot[], boxes: readonly Rect[], size: { w: number; h: number }, height = 20, min = 36): PlacedLabel[] {
  const out: PlacedLabel[] = []
  const rects: Rect[] = [...boxes]
  const row = height + 2
  const sorted = [...spots].sort((a, b) => a.y - b.y || a.key.localeCompare(b.key))
  for (const s of sorted) {
    for (const dy of [0, row, -row, 2 * row, -2 * row, 3 * row, -3 * row]) {
      const top = s.y + dy - height / 2
      if (top < 2 || top + height > size.h - 2) continue
      const w = Math.min(s.width, roomAt(s.x, top, height, s.side, rects, size.w))
      if (w < min) continue
      const left = s.side === 'l' ? s.x - w : s.x
      out.push({ key: s.key, left, top, width: w, height })
      rects.push({ l: left, r: left + w, t: top, b: top + height })
      break
    }
  }
  return out
}

/**
 * The labels of the lines of one app: each at the end of its line (the card
 * that is read), reading away from that card.
 */
export function labelSpots(routes: readonly RoutedLink[], focus: string, textOf: (r: RoutedLink) => string): (LabelSpot & { text: string })[] {
  return routes
    .filter((r) => r.from === focus || r.to === focus)
    .map((r) => {
      const text = textOf(r)
      return { key: r.key, x: r.endSide === 'l' ? r.end.x - 6 : r.end.x + 6, y: r.end.y, side: r.endSide, width: labelWidth(text), text }
    })
}

/** About how wide a label is: 10.5px monospace text and its padding. */
export function labelWidth(text: string): number {
  return Math.ceil(text.length * 6.4 + 16)
}

/** Whether the line between two points runs through the box (its edges count as outside). */
export function crossesBox(a: Pt, b: Pt, box: Pick<Box, 'l' | 'r' | 't' | 'b'>): boolean {
  const eps = 0.5
  if (Math.abs(a.y - b.y) < eps) {
    const [x1, x2] = a.x < b.x ? [a.x, b.x] : [b.x, a.x]
    return a.y > box.t + eps && a.y < box.b - eps && x2 > box.l + eps && x1 < box.r - eps
  }
  if (Math.abs(a.x - b.x) < eps) {
    const [y1, y2] = a.y < b.y ? [a.y, b.y] : [b.y, a.y]
    return a.x > box.l + eps && a.x < box.r - eps && y2 > box.t + eps && y1 < box.b - eps
  }
  return false
}
