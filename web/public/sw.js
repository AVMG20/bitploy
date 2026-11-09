// bitploy's service worker: browser notifications only (Settings,
// Notifications). It caches nothing and does not touch page loads: there is
// no fetch handler. The server sends { title, body, url, tag,
// requireInteraction } (internal/notify/webpush.go, PushPayload).

self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

// Only pages of this dashboard are opened from a notification: a link to
// another origin (the server's public URL when the dashboard is opened at
// another address) keeps its path on this origin.
function sameOrigin(url) {
  try {
    const u = new URL(url || '/', self.location.origin)
    if (u.origin === self.location.origin) return u.href
    return u.protocol === 'https:' || u.protocol === 'http:' ? self.location.origin + u.pathname.replace(/^\/+/, '/') + u.search : self.location.origin + '/'
  } catch {
    return self.location.origin + '/'
  }
}

self.addEventListener('push', (event) => {
  let data
  try {
    data = event.data ? event.data.json() : {}
  } catch {
    data = { body: event.data ? event.data.text() : '' }
  }
  const title = typeof data.title === 'string' && data.title ? data.title : 'bitploy'
  event.waitUntil(
    self.registration.showNotification(title, {
      body: typeof data.body === 'string' ? data.body : '',
      tag: typeof data.tag === 'string' && data.tag ? data.tag : 'bitploy',
      renotify: true,
      requireInteraction: data.requireInteraction === true,
      icon: '/notification-icon.png', // not the SVG favicon: Chrome shows no SVG icon in a notification
      data: { url: sameOrigin(data.url) },
    }),
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const url = sameOrigin(event.notification.data && event.notification.data.url)
  event.waitUntil(
    (async () => {
      const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
      for (const client of windows) {
        if (new URL(client.url).origin !== self.location.origin) continue
        try {
          const focused = await client.focus()
          if (focused.url !== url) await focused.navigate(url)
          return
        } catch {
          break // a page this worker does not control cannot be navigated: open a new one
        }
      }
      await self.clients.openWindow(url)
    })(),
  )
})
