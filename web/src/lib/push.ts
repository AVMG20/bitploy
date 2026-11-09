// Browser notifications (Web Push) in this browser: the service worker
// (public/sw.js) and the push subscription. The pure parts are in
// notifications.ts.

import { api } from './api'
import { base64UrlBytes, currentBrowserChannel, hex, type PushState } from './notifications'

const SW_URL = '/sw.js'

/** Whether this browser can get push notifications here, and the permission. */
export function pushState(): PushState {
  if (!window.isSecureContext) return 'insecure'
  if (!('serviceWorker' in navigator) || !('PushManager' in window) || !('Notification' in window)) return 'unsupported'
  return Notification.permission
}

async function registration(): Promise<ServiceWorkerRegistration | undefined> {
  return navigator.serviceWorker.getRegistration('/')
}

/** The push subscription of this browser, if it has one. */
export async function currentSubscription(): Promise<PushSubscription | null> {
  if (pushState() !== 'granted') return null
  const reg = await registration()
  return (await reg?.pushManager.getSubscription()) ?? null
}

/** Hex SHA-256 of an endpoint: how the API names a browser channel's endpoint. */
export async function endpointHash(endpoint: string): Promise<string> {
  return hex(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(endpoint)))
}

/** The endpoint hash of this browser's subscription, or null. */
export async function currentEndpointHash(): Promise<string | null> {
  try {
    const sub = await currentSubscription()
    return sub ? await endpointHash(sub.endpoint) : null
  } catch {
    return null
  }
}

export class PushDenied extends Error {}

/**
 * Asks for permission, registers the service worker and subscribes with the
 * server's VAPID key. Throws PushDenied when the user says no.
 */
export async function subscribe(vapidKey: string): Promise<PushSubscriptionJSON> {
  const permission = await Notification.requestPermission()
  if (permission !== 'granted') throw new PushDenied('Notifications were not allowed.')
  await navigator.serviceWorker.register(SW_URL, { scope: '/', updateViaCache: 'none' })
  const reg = await navigator.serviceWorker.ready
  const key = base64UrlBytes(vapidKey)
  let sub = await reg.pushManager.getSubscription()
  if (sub && !sameKey(sub.options.applicationServerKey, key)) {
    await sub.unsubscribe() // made with another server key: it cannot be used
    sub = null
  }
  sub ??= await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key })
  return sub.toJSON()
}

function sameKey(a: ArrayBuffer | null, b: Uint8Array): boolean {
  if (!a) return false
  return hex(a) === hex(b)
}

/** Ends this browser's subscription, so nothing more arrives here. */
export async function unsubscribe(): Promise<void> {
  try {
    const sub = await currentSubscription()
    await sub?.unsubscribe()
  } catch {
    // nothing to end
  }
}

/**
 * On logout: deletes the channel of this browser and ends its subscription,
 * so the next person on this browser does not see the notifications of the
 * account that logged out. Never throws.
 */
export async function forgetThisBrowser(): Promise<void> {
  try {
    const hash = await currentEndpointHash()
    if (!hash) return
    const mine = currentBrowserChannel(await api.notificationChannels(), hash)
    if (!mine) return
    await api.deleteNotificationChannel(mine.id)
    await unsubscribe()
  } catch {
    // logging out goes on
  }
}

/**
 * On "log out everywhere": deletes the channels of all your browsers (a
 * lost laptop gets no more notifications) and ends this browser's
 * subscription. Never throws.
 */
export async function forgetAllBrowsers(): Promise<void> {
  try {
    const browsers = (await api.notificationChannels()).filter((c) => c.type === 'browser')
    await Promise.all(browsers.map((c) => api.deleteNotificationChannel(c.id)))
    await unsubscribe()
  } catch {
    // logging out goes on
  }
}
