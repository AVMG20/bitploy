// The lines on the project canvas: from each app to the apps whose variables
// it reads. Cards sit in columns (the lanes); a line leaves a card from the
// side that faces the other card, runs up or down in the gap between two
// columns, and never through a card. A line that has to pass a column on the
// way goes along beside it when there is room, else below it.

export interface Pt {
  x: number
  y: number
}

/** A card on the board, in board coordinates, and the column (lane) it is in. */
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
  endSide: 'l' | 'r'
}

type Side = 'l' | 'r'

/** How far below a card's top the first line leaves it (under the name). */
const PORT_TOP = 34
/** Room kept around a card a line passes by. */
const CLEAR = 8
/** The room right of the last column for lines between cards of that column. */
const EDGE = 28

interface Vertical {
  link: number
  gutter: number
  y1: number
  y2: number
  /** The way the line goes after this part: toward a column on the right, or on the left. */
  exit: Side
  x: number
}

/** The x range of the gap right of column g (the last column: the room beside it). */
function gutters(boxes: readonly Box[]): { l: number; r: number }[] {
  const cols = Math.max(-1, ...boxes.map((b) => b.col)) + 1
  const left: number[] = []
  const right: number[] = []
  for (let c = 0; c < cols; c++) {
    const inCol = boxes.filter((b) => b.col === c)
    left.push(inCol.length ? Math.min(...inCol.map((b) => b.l)) : NaN)
    right.push(inCol.length ? Math.max(...inCol.map((b) => b.r)) : NaN)
  }
  return right.map((r, g) => ({ l: r, r: g + 1 < cols ? left[g + 1]! : r + EDGE }))
}

/**
 * Route every link between the boxes. Links naming a box that is not there
 * are left out.
 */
export function routeLinks(boxes: readonly Box[], links: readonly { from: string; to: string }[], radius = 8): RoutedLink[] {
  const byId = new Map(boxes.map((b) => [b.id, b]))
  const gaps = gutters(boxes)

  // The sides each line leaves and enters by.
  interface End {
    id: string
    side: Side
    /** The middle of the other card, so lines to cards higher up leave higher. */
    toward: number
    y: number
  }
  const plans: { i: number; a: Box; b: Box; ea: End; eb: End }[] = []
  links.forEach((l, i) => {
    const a = byId.get(l.from)
    const b = byId.get(l.to)
    if (!a || !b || a === b) return
    const [sa, sb]: [Side, Side] = a.col < b.col ? ['r', 'l'] : a.col > b.col ? ['l', 'r'] : ['r', 'r']
    plans.push({ i, a, b, ea: { id: a.id, side: sa, toward: (b.t + b.b) / 2, y: 0 }, eb: { id: b.id, side: sb, toward: (a.t + a.b) / 2, y: 0 } })
  })

  // Several lines on one side of a card spread out instead of meeting in one point.
  const bySide = new Map<string, End[]>()
  for (const p of plans) {
    for (const e of [p.ea, p.eb]) bySide.set(`${e.id}:${e.side}`, [...(bySide.get(`${e.id}:${e.side}`) ?? []), e])
  }
  for (const ends of bySide.values()) {
    const box = byId.get(ends[0]!.id)!
    ends.sort((x, y) => x.toward - y.toward)
    const room = box.b - box.t - PORT_TOP - 12
    const step = Math.min(22, Math.max(10, room / Math.max(1, ends.length)))
    ends.forEach((e, k) => (e.y = Math.min(box.b - 12, box.t + PORT_TOP + k * step)))
  }

  // Where each line runs up or down: the gaps between columns.
  const verticals: Vertical[] = []
  const corridors = new Map<string, number>()
  const routes: { p: (typeof plans)[number]; start: Pt; end: Pt; parts: Vertical[] }[] = []
  for (const p of plans) {
    const { a, b, ea, eb } = p
    const start = { x: ea.side === 'r' ? a.r : a.l, y: ea.y }
    const end = { x: eb.side === 'r' ? b.r : b.l, y: eb.y }
    const exit: Side = a.col <= b.col ? 'r' : 'l'
    const parts: Vertical[] = []
    const part = (gutter: number, y1: number, y2: number, dir: Side) => parts.push({ link: p.i, gutter, y1, y2, exit: dir, x: 0 })
    if (a.col === b.col) {
      part(a.col, start.y, end.y, 'l')
    } else {
      const lo = Math.min(a.col, b.col)
      const hi = Math.max(a.col, b.col)
      // The gap next to each card.
      const first = a.col < b.col ? a.col : a.col - 1
      const last = a.col < b.col ? b.col - 1 : b.col
      const between = boxes.filter((x) => x.col > lo && x.col < hi)
      const clear = (y: number) => between.every((x) => y < x.t - CLEAR || y > x.b + CLEAR)
      if (!between.length) part(first, start.y, end.y, exit)
      else if (clear(start.y)) part(last, start.y, end.y, exit)
      else if (clear(end.y)) part(first, start.y, end.y, exit)
      else {
        // Below the columns in between.
        const k = `${lo}-${hi}`
        const n = corridors.get(k) ?? 0
        corridors.set(k, n + 1)
        const y = Math.max(...between.map((x) => x.b)) + 14 + n * 8
        part(first, start.y, y, exit)
        part(last, y, end.y, exit)
      }
    }
    verticals.push(...parts)
    routes.push({ p, start, end, parts })
  }

  // Each line gets its own track in a gap. A line turning right toward a lower
  // card runs left of one turning toward a higher card, so they do not cross.
  for (let g = 0; g < gaps.length; g++) {
    const inGap = verticals.filter((v) => v.gutter === g)
    if (!inGap.length) continue
    inGap.sort((x, y) => (x.exit === y.exit ? (x.exit === 'r' ? y.y2 - x.y2 : x.y2 - y.y2) : x.exit === 'r' ? -1 : 1))
    const { l, r } = gaps[g]!
    const width = r - l
    const step = inGap.length > 1 ? Math.min(10, Math.max(3, (width - 20) / (inGap.length - 1))) : 0
    const mid = (l + r) / 2
    inGap.forEach((v, k) => (v.x = mid + (k - (inGap.length - 1) / 2) * step))
  }

  return routes.map(({ p, start, end, parts }) => {
    const points: Pt[] = [start]
    for (const v of parts) points.push({ x: v.x, y: v.y1 }, { x: v.x, y: v.y2 })
    points.push(end)
    const l = links[p.i]!
    return { key: `${l.from}>${l.to}`, from: l.from, to: l.to, points, d: roundedPath(points, radius), end, endSide: p.eb.side }
  })
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

export interface Label {
  key: string
  x: number
  y: number
  /** Which way the label reads from its point: 'l' ends at x, 'r' starts at x. */
  side: Side
}

/** Labels that share a spot move apart, top to bottom, at least `height` apart. */
export function spreadLabels<T extends Label>(labels: readonly T[], height = 22): T[] {
  const groups = new Map<string, T[]>()
  for (const l of labels) {
    const k = `${Math.round(l.x)}:${l.side}`
    groups.set(k, [...(groups.get(k) ?? []), l])
  }
  const out: T[] = []
  for (const g of groups.values()) {
    const sorted = [...g].sort((a, b) => a.y - b.y)
    let last = -Infinity
    for (const l of sorted) {
      const y = Math.max(l.y, last + height)
      out.push({ ...l, y })
      last = y
    }
  }
  return out
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
