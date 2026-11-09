// The service worker of browser notifications (public/sw.js), run against a
// fake `self`: what a push shows and where a click goes.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const source = readFileSync(new URL('../public/sw.js', import.meta.url), 'utf8')
const ORIGIN = 'https://bitploy.example'

type Listener = (event: unknown) => void
interface Shown {
  title: string
  options: { body: string; tag: string; requireInteraction: boolean; data: { url: string } }
}
interface FakeClient {
  url: string
  focus: () => Promise<FakeClient>
  navigate: (url: string) => Promise<void>
}

function load(windows: FakeClient[] = []) {
  const listeners: Record<string, Listener> = {}
  const shown: Shown[] = []
  const opened: string[] = []
  const self = {
    location: { origin: ORIGIN },
    addEventListener: (type: string, fn: Listener) => (listeners[type] = fn),
    skipWaiting: () => {},
    registration: {
      showNotification: async (title: string, options: Shown['options']) => {
        shown.push({ title, options })
      },
    },
    clients: {
      claim: async () => {},
      matchAll: async () => windows,
      openWindow: async (url: string) => {
        opened.push(url)
      },
    },
  }
  new Function('self', source)(self)
  return { listeners, shown, opened }
}

async function push(sw: ReturnType<typeof load>, data: unknown) {
  let done: Promise<unknown> = Promise.resolve()
  sw.listeners.push({
    data: data === undefined ? null : { json: () => (typeof data === 'string' ? JSON.parse(data) : data), text: () => String(data) },
    waitUntil: (p: Promise<unknown>) => (done = p),
  })
  await done
}

async function click(sw: ReturnType<typeof load>, url: string) {
  let done: Promise<unknown> = Promise.resolve()
  let closed = false
  sw.listeners.notificationclick({
    notification: { data: { url }, close: () => (closed = true) },
    waitUntil: (p: Promise<unknown>) => (done = p),
  })
  await done
  return closed
}

test('a push shows the title, body and tag the server sent', async () => {
  const sw = load()
  await push(sw, { title: 'Deployment failed: shop', body: 'The build failed.', url: `${ORIGIN}/apps/a1`, tag: 'bitploy:a1', requireInteraction: true })
  assert.equal(sw.shown.length, 1)
  const [n] = sw.shown
  assert.equal(n.title, 'Deployment failed: shop')
  assert.equal(n.options.body, 'The build failed.')
  assert.equal(n.options.tag, 'bitploy:a1')
  assert.equal(n.options.requireInteraction, true)
  assert.equal(n.options.data.url, `${ORIGIN}/apps/a1`)
})

test('a push without usable data still shows a notification', async () => {
  const sw = load()
  await push(sw, undefined)
  await push(sw, 'not json')
  assert.deepEqual(
    sw.shown.map((n) => [n.title, n.options.body, n.options.tag, n.options.requireInteraction]),
    [
      ['bitploy', '', 'bitploy', false],
      ['bitploy', 'not json', 'bitploy', false],
    ],
  )
})

test('a link to another site becomes the dashboard home', async () => {
  const sw = load()
  await push(sw, { title: 't', url: 'https://evil.example/phish' })
  await push(sw, { title: 't', url: 'javascript:alert(1)' })
  await push(sw, { title: 't', url: '/apps/a1' })
  assert.deepEqual(
    sw.shown.map((n) => n.options.data.url),
    [`${ORIGIN}/`, `${ORIGIN}/`, `${ORIGIN}/apps/a1`],
  )
})

test('a click focuses an open dashboard tab and goes to the link', async () => {
  const navigated: string[] = []
  const tab: FakeClient = {
    url: `${ORIGIN}/projects`,
    focus: async () => tab,
    navigate: async (url) => {
      navigated.push(url)
    },
  }
  const sw = load([tab])
  assert.equal(await click(sw, `${ORIGIN}/apps/a1`), true)
  assert.deepEqual(navigated, [`${ORIGIN}/apps/a1`])
  assert.deepEqual(sw.opened, [])
})

test('a click opens a new window when no dashboard tab is open', async () => {
  const other: FakeClient = { url: 'https://other.example/', focus: async () => other, navigate: async () => {} }
  const sw = load([other])
  await click(sw, 'https://evil.example/')
  assert.deepEqual(sw.opened, [`${ORIGIN}/`])
})
