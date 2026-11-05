import assert from 'node:assert/strict'
import { test } from 'node:test'
import { crossesBox, roundedPath, routeLinks, spreadLabels } from '../src/lib/canvas.ts'
import type { Box, RoutedLink } from '../src/lib/canvas.ts'

// Three lanes of 272px with 64px gaps, like the canvas at desktop width.
const col = (c: number) => ({ l: 32 + c * 336, r: 32 + c * 336 + 272 })
const box = (id: string, c: number, t: number, h = 180): Box => ({ id, col: c, ...col(c), t, b: t + h })

const web = box('web', 0, 60)
const api = box('api', 1, 60)
const worker = box('worker', 1, 252)
const pg = box('pg', 2, 60)
const redis = box('redis', 2, 252)
const boxes = [web, api, worker, pg, redis]

function hits(r: RoutedLink, all: readonly Box[]): string[] {
  const out: string[] = []
  for (let i = 0; i < r.points.length - 1; i++) {
    for (const b of all) if (crossesBox(r.points[i]!, r.points[i + 1]!, b)) out.push(b.id)
  }
  return out
}

test('routeLinks runs between columns and never through a card', () => {
  const routes = routeLinks(boxes, [
    { from: 'web', to: 'api' },
    { from: 'api', to: 'pg' },
    { from: 'api', to: 'redis' },
    { from: 'worker', to: 'pg' },
    { from: 'web', to: 'pg' },
    { from: 'web', to: 'redis' },
  ])
  assert.equal(routes.length, 6)
  for (const r of routes) {
    assert.deepEqual(hits(r, boxes), [], `${r.key} crosses a card`)
    // Only straight runs: every part is level or upright.
    for (let i = 0; i < r.points.length - 1; i++) {
      const a = r.points[i]!
      const b = r.points[i + 1]!
      assert.ok(a.x === b.x || a.y === b.y, `${r.key} has a slanted part`)
    }
  }
  const wp = routes.find((r) => r.key === 'web>pg')!
  // web and pg are two columns apart, with api in the way: the line goes below the middle column.
  assert.ok(wp.points.some((p) => p.y > Math.max(api.b, worker.b)), 'web>pg goes below the middle column')
  assert.equal(wp.endSide, 'l')
  assert.equal(wp.end.x, pg.l)
})

test('routeLinks gives each line its own track in a gap', () => {
  const routes = routeLinks(boxes, [
    { from: 'api', to: 'pg' },
    { from: 'api', to: 'redis' },
    { from: 'worker', to: 'pg' },
  ])
  const xs = routes.map((r) => r.points[1]!.x)
  assert.equal(new Set(xs).size, 3)
  for (const x of xs) assert.ok(x > api.r && x < pg.l, 'the upright part is in the gap')
  // Two lines on one side of a card leave it at different heights.
  const ys = routes.filter((r) => r.from === 'api').map((r) => r.points[0]!.y)
  assert.notEqual(ys[0], ys[1])
})

test('routeLinks: cards in one column join on the right, leftward links leave on the left', () => {
  const [same] = routeLinks(boxes, [{ from: 'api', to: 'worker' }])
  assert.equal(same!.points[0]!.x, api.r)
  assert.equal(same!.end.x, worker.r)
  assert.equal(same!.endSide, 'r')
  assert.ok(same!.points[1]!.x > api.r)

  const [back] = routeLinks(boxes, [{ from: 'pg', to: 'web' }])
  assert.equal(back!.points[0]!.x, pg.l)
  assert.equal(back!.end.x, web.r)
  assert.deepEqual(hits(back!, boxes), [])
})

test('routeLinks goes beside the middle column when there is room', () => {
  // A short middle column: the line runs level below it.
  const shortApi = box('api', 1, 60, 60)
  const tallWeb = box('web', 0, 60, 300)
  const lowPg = box('pg', 2, 200)
  const [r] = routeLinks([tallWeb, shortApi, lowPg], [{ from: 'web', to: 'pg' }])
  assert.deepEqual(hits(r!, [tallWeb, shortApi, lowPg]), [])
  assert.equal(r!.points.length, 4)
})

test('routeLinks leaves out links to cards that are not on the board', () => {
  assert.deepEqual(routeLinks(boxes, [{ from: 'web', to: 'gone' }]), [])
  assert.deepEqual(routeLinks([], [{ from: 'a', to: 'b' }]), [])
})

test('roundedPath rounds the corners and drops straight runs', () => {
  assert.equal(roundedPath([{ x: 0, y: 0 }, { x: 50, y: 0 }, { x: 100, y: 0 }]), 'M0,0 L100,0')
  assert.equal(roundedPath([{ x: 0, y: 0 }, { x: 50, y: 0 }, { x: 50, y: 40 }], 8), 'M0,0 L42,0 Q50,0 50,8 L50,40')
  // A short part makes a smaller corner.
  assert.equal(roundedPath([{ x: 0, y: 0 }, { x: 4, y: 0 }, { x: 4, y: 40 }], 8), 'M0,0 L2,0 Q4,0 4,2 L4,40')
  assert.equal(roundedPath([]), '')
})

test('spreadLabels moves labels at one spot apart', () => {
  const out = spreadLabels(
    [
      { key: 'a', x: 100, y: 50, side: 'l' as const },
      { key: 'b', x: 100, y: 60, side: 'l' as const },
      { key: 'c', x: 300, y: 55, side: 'l' as const },
    ],
    22,
  )
  const y = Object.fromEntries(out.map((l) => [l.key, l.y]))
  assert.equal(y.a, 50)
  assert.equal(y.b, 72)
  assert.equal(y.c, 55)
})

/** Points where a level part of one line crosses an upright part of the other. */
function crossingsBetween(a: RoutedLink, b: RoutedLink): number {
  let n = 0
  const parts = (r: RoutedLink) => r.points.slice(1).map((p, i) => [r.points[i]!, p] as const)
  for (const [p1, p2] of parts(a)) {
    for (const [q1, q2] of parts(b)) {
      const [h, v] = p1.y === p2.y && q1.x === q2.x ? [[p1, p2], [q1, q2]] : q1.y === q2.y && p1.x === p2.x ? [[q1, q2], [p1, p2]] : [null, null]
      if (!h || !v) continue
      const x = v[0]!.x
      const y = h[0]!.y
      const inX = x > Math.min(h[0]!.x, h[1]!.x) + 0.5 && x < Math.max(h[0]!.x, h[1]!.x) - 0.5
      const inY = y > Math.min(v[0]!.y, v[1]!.y) + 0.5 && y < Math.max(v[0]!.y, v[1]!.y) - 0.5
      if (inX && inY) n++
    }
  }
  return n
}

function assertNoCrossings(routes: readonly RoutedLink[]) {
  for (let i = 0; i < routes.length; i++) {
    for (let j = i + 1; j < routes.length; j++) assert.equal(crossingsBetween(routes[i]!, routes[j]!), 0, `${routes[i]!.key} crosses ${routes[j]!.key}`)
  }
}

test('routeLinks: a line that goes below a column leaves the straight lines next to it straight', () => {
  // web reads api and pg, api reads pg; api is taller than pg, so web>pg goes below api.
  const w = box('web', 0, 40, 230)
  const a = box('api', 1, 40, 256)
  const p = box('pg', 2, 40, 230)
  const routes = routeLinks([w, a, p], [
    { from: 'web', to: 'api' },
    { from: 'web', to: 'pg' },
    { from: 'api', to: 'pg' },
  ])
  for (const r of routes.filter((x) => x.key !== 'web>pg')) assert.ok(r.points.every((pt) => pt.y === r.points[0]!.y), `${r.key} is one level line`)
  assertNoCrossings(routes)
})

test('routeLinks: a line running level past a column enters below the lines turning in the last gap', () => {
  // The Shop project: web reads api and postgres, api reads redis and postgres; postgres sits under redis.
  const routes = routeLinks(
    [box('web', 0, 311, 228), box('api', 1, 311, 256), box('redis', 2, 311, 230), box('pg', 2, 555, 230)],
    [
      { from: 'web', to: 'api' },
      { from: 'web', to: 'pg' },
      { from: 'api', to: 'redis' },
      { from: 'api', to: 'pg' },
    ],
  )
  assertNoCrossings(routes)
})

test('routeLinks orders the tracks in a gap so lines do not cross', () => {
  const routes = routeLinks(
    [box('web', 0, 40, 230), box('api', 1, 40, 256), box('worker', 1, 310, 200), box('pg', 2, 40, 230)],
    [
      { from: 'web', to: 'api' },
      { from: 'web', to: 'pg' },
      { from: 'worker', to: 'pg' },
      { from: 'api', to: 'pg' },
    ],
  )
  assertNoCrossings(routes)
})
