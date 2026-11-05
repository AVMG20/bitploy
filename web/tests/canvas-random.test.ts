// Randomized checks of the canvas lines: many made-up projects, laid out the
// way the canvas lays them out at several widths, and what must hold for each.
//
// "No crossing where it can be avoided" is checked three ways:
// 1. Lines never run on top of each other (no two parallel runs closer than
//    NEAR px along a stretch), and two lines cross at most once.
// 2. Two lines between the same two neighbouring lanes cross only when their
//    cards force it: one line's card is above the other's in one lane and
//    below it in the other.
// 3. Nothing a reader would fix by hand helps: swapping the upright runs of
//    two lines in one gap, or the ports of two lines on one side of a card,
//    never gives fewer crossings (without running into a card, onto a line,
//    or into a crossing 1 or 2 rules out).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { crossesBox, labelSpots, placeLabels, routeLinks, routesBottom } from '../src/lib/canvas.ts'
import type { Box, Pt, RoutedLink } from '../src/lib/canvas.ts'
import { BOARDS, crossings, layout, overlaps, segs } from './canvas-layouts.ts'
import type { Layout } from './canvas-layouts.ts'

const hitsBox = (pts: readonly Pt[], boxes: readonly Box[]) => segs(pts).some((s) => boxes.some((b) => crossesBox(s.a, s.b, b)))

interface Stats {
  layouts: number
  lines: number
  crossings: number
  /** Labels in the gaps between lanes, and how many of them found room. */
  labels: number
  shown: number
  grown: number
}

function check(L: Layout, label: string, stats: Stats) {
  const boxes = [...L.cards, ...L.extra]
  const routes = routeLinks(boxes, L.links, { width: L.w })
  const byId = new Map(L.cards.map((b) => [b.id, b]))
  const pts = routes.map((r) => r.points)
  const bottom = Math.max(L.h, routesBottom(routes) + 16)
  stats.layouts++
  stats.lines += routes.length
  if (bottom > L.h) stats.grown++
  assert.equal(routes.length, L.links.length, `${label}: every link has a line`)

  routes.forEach((r) => {
    const a = byId.get(r.from)!
    const b = byId.get(r.to)!
    const p = r.points
    // Level and upright runs only, from an edge of one card to an edge of the other.
    for (const s of segs(p)) assert.ok(Math.abs(s.a.x - s.b.x) < 0.01 || Math.abs(s.a.y - s.b.y) < 0.01, `${label}: ${r.key} has a slanted run`)
    assert.ok((p[0]!.x === a.l || p[0]!.x === a.r) && p[0]!.y > a.t && p[0]!.y < a.b, `${label}: ${r.key} starts on its card's side`)
    assert.ok((r.end.x === b.l || r.end.x === b.r) && r.end.y > b.t && r.end.y < b.b, `${label}: ${r.key} ends on its card's side`)
    // Never through a card or the add button, its own cards included.
    assert.ok(!hitsBox(p, boxes), `${label}: ${r.key} runs through a card`)
    // Inside the board (grown below when lines pass under a lane), never over the lane titles.
    for (const q of p) {
      assert.ok(q.x >= 1 && q.x <= L.w - 1, `${label}: ${r.key} leaves the board at x=${q.x}`)
      assert.ok(q.y >= L.top && q.y <= bottom - 8, `${label}: ${r.key} leaves the board at y=${q.y}`)
    }
  })

  // 1. Never on top of each other; at most one crossing per pair.
  const cross: number[][] = routes.map(() => routes.map(() => 0))
  for (let i = 0; i < routes.length; i++) {
    for (let j = i + 1; j < routes.length; j++) {
      assert.equal(overlaps(pts[i]!, pts[j]!), 0, `${label}: ${routes[i]!.key} runs on top of ${routes[j]!.key}`)
      const c = crossings(pts[i]!, pts[j]!)
      assert.ok(c <= 1, `${label}: ${routes[i]!.key} crosses ${routes[j]!.key} ${c} times`)
      cross[i]![j] = cross[j]![i] = c
      stats.crossings += c
    }
  }

  // 2. Lines between two neighbouring lanes cross only when their cards force it.
  /** Two lines between the same neighbouring lanes whose cards do not force a crossing. */
  const needless = (i: number, j: number) => {
    const ends = (x: RoutedLink) => [byId.get(x.from)!, byId.get(x.to)!].sort((p, q) => p.col - q.col)
    const [a1, a2] = ends(routes[i]!)
    const [b1, b2] = ends(routes[j]!)
    if (a2!.col - a1!.col !== 1 || a1!.col !== b1!.col || a2!.col !== b2!.col) return false
    return !(a1 !== b1 && a2 !== b2 && Math.sign(a1!.t - b1!.t) !== Math.sign(a2!.t - b2!.t))
  }
  for (let i = 0; i < routes.length; i++) {
    for (let j = i + 1; j < routes.length; j++) {
      if (!cross[i]![j]) continue
      assert.ok(!needless(i, j), `${label}: ${routes[i]!.key} and ${routes[j]!.key} cross though their cards do not force it`)
    }
  }

  // 3. No swap of two upright runs in a gap or two ports on a card side helps.
  /** The crossings of a moved line with the lines that did not move, and whether any of them is one rules 1 and 2 rule out. */
  const others = (k: number, q: Pt[], skip: number) => {
    let c = 0
    let bad = false
    for (let j = 0; j < routes.length; j++) {
      if (j === k || j === skip) continue
      const n = crossings(q, pts[j]!)
      c += n
      if (n > 1 || (n && needless(k, j)) || overlaps(q, pts[j]!)) bad = true
    }
    return { c, bad }
  }
  const before = (i: number, j: number) => routes.reduce((s, _, k) => s + (k !== i ? cross[i]![k]! : 0) + (k !== j && k !== i ? cross[j]![k]! : 0), 0)
  const tryBoth = (i: number, j: number, qi: Pt[], qj: Pt[], what: string) => {
    if (hitsBox(qi, boxes) || hitsBox(qj, boxes)) return
    const own = crossings(qi, qj)
    if (overlaps(qi, qj) || own > 1 || (own && needless(i, j))) return
    const a = others(i, qi, j)
    const b = others(j, qj, i)
    if (a.bad || b.bad) return
    const after = a.c + b.c + own
    assert.ok(after >= before(i, j), `${label}: swapping ${what} of ${routes[i]!.key} and ${routes[j]!.key} gives fewer crossings (${before(i, j)} to ${after})`)
  }
  // Upright runs: points 1-2 (and 3-4 for a line going below a lane), grouped by gap.
  const lefts = [...new Set(L.cards.map((b) => b.l))].sort((x, y) => x - y)
  const gapOf = (x: number) => lefts.filter((l) => l < x).length
  const uprights: { line: number; at: number; x: number; gap: number }[] = []
  routes.forEach((r, k) => {
    for (let at = 1; at + 1 < r.points.length - 1; at += 2) uprights.push({ line: k, at, x: r.points[at]!.x, gap: gapOf(r.points[at]!.x) })
  })
  for (const u of uprights) {
    for (const v of uprights) {
      if (u.line >= v.line || u.gap !== v.gap) continue
      const qi = pts[u.line]!.map((q) => ({ ...q }))
      const qj = pts[v.line]!.map((q) => ({ ...q }))
      qi[u.at]!.x = qi[u.at + 1]!.x = v.x
      qj[v.at]!.x = qj[v.at + 1]!.x = u.x
      tryBoth(u.line, v.line, qi, qj, 'the upright runs')
    }
  }
  // Ports: the first or last point and the bend next to it.
  const ports = new Map<string, { line: number; first: boolean; y: number }[]>()
  routes.forEach((r, k) => {
    const a = byId.get(r.from)!
    const b = byId.get(r.to)!
    const p = r.points
    for (const [box, q, first] of [
      [a, p[0]!, true],
      [b, p.at(-1)!, false],
    ] as const) {
      const key = `${box.id}:${q.x === box.l ? 'l' : 'r'}`
      ports.set(key, [...(ports.get(key) ?? []), { line: k, first, y: q.y }])
    }
  })
  for (const list of ports.values()) {
    for (const u of list) {
      for (const v of list) {
        if (u.line >= v.line) continue
        const qi = pts[u.line]!.map((q) => ({ ...q }))
        const qj = pts[v.line]!.map((q) => ({ ...q }))
        const move = (q: Pt[], first: boolean, y: number) => {
          const [e, bend] = first ? [0, 1] : [q.length - 1, q.length - 2]
          q[e]!.y = q[bend]!.y = y
        }
        move(qi, u.first, v.y)
        move(qj, v.first, u.y)
        tryBoth(u.line, v.line, qi, qj, 'the ports')
      }
    }
  }

  // Labels: for every app, the labels of its lines cover no card, no other label, and stay on the board.
  const names = ['DATABASE_URL', 'REDIS_URL', 'API_URL', 'MEILI_URL, MEILI_MASTER_KEY', 'S3_ENDPOINT, S3_KEY, S3_SECRET']
  for (const c of L.cards) {
    const spots = labelSpots(routes, c.id, (r) => names[(r.key.length + r.from.length) % names.length]!)
    const placed = placeLabels(spots, boxes, { w: L.w, h: bottom })
    // Labels in the narrow margins beside the first and last lane have no room; the card names those variables.
    const inGap = spots.filter((sp) => sp.x > lefts[0]! && sp.x < Math.max(...L.cards.map((b) => b.r)))
    stats.labels += inGap.length
    stats.shown += placed.filter((p) => inGap.some((sp) => sp.key === p.key)).length
    for (const p of placed) {
      const rect = { l: p.left, r: p.left + p.width, t: p.top, b: p.top + p.height }
      assert.ok(rect.l >= 0 && rect.r <= L.w && rect.t >= 0 && rect.b <= bottom, `${label}: label ${p.key} leaves the board`)
      for (const b of boxes) assert.ok(rect.r <= b.l || rect.l >= b.r || rect.b <= b.t || rect.t >= b.b, `${label}: label ${p.key} covers ${b.id}`)
      for (const q of placed) {
        if (q === p) continue
        assert.ok(rect.r <= q.left || rect.l >= q.left + q.width || rect.b <= q.top || rect.t >= q.top + q.height, `${label}: labels ${p.key} and ${q.key} overlap`)
      }
    }
  }
}

const RUNS = Number(process.env.CANVAS_RUNS ?? 100)

for (const board of BOARDS) {
  test(`routeLinks on ${RUNS} random projects at ${board.name}`, () => {
    const stats: Stats = { layouts: 0, lines: 0, crossings: 0, labels: 0, shown: 0, grown: 0 }
    for (let seed = 1; seed <= RUNS; seed++) check(layout(seed * 7919 + board.w, board), `${board.name} seed ${seed}`, stats)
    // Most labels find room; a label is left out only where a narrow gap is crowded.
    if (board.w >= 900) assert.ok(stats.shown >= stats.labels * 0.9, `only ${stats.shown} of ${stats.labels} labels shown`)
    if (process.env.CANVAS_STATS) console.log(board.name, stats)
  })
}

test('routeLinks: lines between neighbouring lanes whose cards do not interleave never cross', () => {
  // Links only between neighbouring lanes, dropping any whose cards would interleave with one kept.
  for (let seed = 1; seed <= 200; seed++) {
    const L = layout(seed * 31, BOARDS[0]!)
    const byId = new Map(L.cards.map((b) => [b.id, b]))
    const kept: { from: string; to: string }[] = []
    const interleave = (x: { from: string; to: string }, y: { from: string; to: string }) => {
      const e = (l: typeof x) => [byId.get(l.from)!, byId.get(l.to)!].sort((p, q) => p.col - q.col)
      const [a1, a2] = e(x)
      const [b1, b2] = e(y)
      if (a1!.col !== b1!.col || a2!.col !== b2!.col) return false
      return a1 !== b1 && a2 !== b2 && Math.sign(a1!.t - b1!.t) !== Math.sign(a2!.t - b2!.t)
    }
    for (const l of L.links) {
      if (Math.abs(byId.get(l.from)!.col - byId.get(l.to)!.col) !== 1) continue
      if (kept.some((k) => interleave(k, l))) continue
      kept.push(l)
    }
    const routes = routeLinks([...L.cards, ...L.extra], kept, { width: L.w })
    for (let i = 0; i < routes.length; i++)
      for (let j = i + 1; j < routes.length; j++) assert.equal(crossings(routes[i]!.points, routes[j]!.points), 0, `seed ${seed}: ${routes[i]!.key} crosses ${routes[j]!.key}`)
  }
})
