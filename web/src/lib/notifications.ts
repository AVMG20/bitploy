// Notification channels (Settings, Notifications). Pure on purpose (types
// only), so Node runs its test: tests/notifications.test.ts.

import type { NotificationChannel, NotificationChannelType, NotificationEvent, NotificationEventGroup } from './types'

export const EVENT_GROUPS: { group: NotificationEventGroup; label: string }[] = [
  { group: 'deployments', label: 'Deployments' },
  { group: 'backups', label: 'Backups' },
  { group: 'app', label: 'App' },
]

export interface EventGroup {
  group: NotificationEventGroup
  label: string
  events: NotificationEvent[]
}

/** The events by group, in the order of EVENT_GROUPS; empty groups are left out. */
export function groupEvents(events: NotificationEvent[]): EventGroup[] {
  return EVENT_GROUPS.map((g) => ({ ...g, events: events.filter((e) => e.group === g.group) })).filter((g) => g.events.length > 0)
}

/** The events a new channel starts with. */
export function defaultEvents(events: NotificationEvent[]): string[] {
  return events.filter((e) => e.default).map((e) => e.type)
}

/** Adds or removes one event, keeping the order of the catalog. */
export function toggleEvent(selected: string[], type: string, on: boolean, catalog: NotificationEvent[]): string[] {
  const set = new Set(selected)
  if (on) set.add(type)
  else set.delete(type)
  return catalog.map((e) => e.type).filter((t) => set.has(t))
}

const DISCORD_HOSTS = new Set(['discord.com', 'discordapp.com', 'ptb.discord.com', 'canary.discord.com', 'ptb.discordapp.com', 'canary.discordapp.com'])

/**
 * What is wrong with a Discord webhook URL, or '' when it looks right. The
 * server checks it again (internal/notify/discord.go ParseDiscordWebhook).
 */
export function discordWebhookProblem(raw: string): string {
  const v = raw.trim()
  if (!v) return 'Paste the webhook URL of the Discord channel.'
  let u: URL
  try {
    u = new URL(v)
  } catch {
    return 'This is not a URL. Copy the webhook URL from Discord.'
  }
  if (u.protocol !== 'https:') return 'The URL must start with https://.'
  if (!DISCORD_HOSTS.has(u.hostname.toLowerCase()) || u.port || u.username || u.password) return 'The address must be discord.com.'
  if (u.search || u.hash || !/^\/api\/webhooks\/\d{5,30}\/[A-Za-z0-9_-]{20,200}$/.test(u.pathname)) {
    return 'This is not a webhook URL. It looks like https://discord.com/api/webhooks/123456789012345678/abc...'
  }
  return ''
}

export type ChannelHealth = 'ok' | 'failing' | 'unused'

/** Whether the last message to the channel arrived. */
export function channelHealth(c: Pick<NotificationChannel, 'last_success_at' | 'last_error' | 'last_error_at'>): ChannelHealth {
  if (c.last_error && c.last_error_at) {
    if (!c.last_success_at || Date.parse(c.last_error_at) >= Date.parse(c.last_success_at)) return 'failing'
  }
  return c.last_success_at ? 'ok' : 'unused'
}

/** "Chrome on macOS" from a user agent; the default name of a browser channel. */
export function browserName(ua: string): string {
  let browser = 'Browser'
  if (/Edg\//.test(ua)) browser = 'Edge'
  else if (/OPR\/|Opera/.test(ua)) browser = 'Opera'
  else if (/Firefox\//.test(ua)) browser = 'Firefox'
  else if (/Chrome\/|CriOS\//.test(ua)) browser = 'Chrome'
  else if (/Safari\//.test(ua)) browser = 'Safari'
  let os = ''
  if (/iPhone|iPad|iPod/.test(ua)) os = 'iOS'
  else if (/Android/.test(ua)) os = 'Android'
  else if (/Mac OS X|Macintosh/.test(ua)) os = 'macOS'
  else if (/Windows/.test(ua)) os = 'Windows'
  else if (/CrOS/.test(ua)) os = 'ChromeOS'
  else if (/Linux/.test(ua)) os = 'Linux'
  return os ? `${browser} on ${os}` : browser
}

/** How a channel type announces an emergency event, as a short mark next to it. */
export function emergencyMark(type: NotificationChannelType): string {
  return type === 'discord' ? 'pings @everyone' : 'stays on screen'
}

/** One line about where a channel's messages go. */
export function channelTarget(c: Pick<NotificationChannel, 'type' | 'config'>): string {
  if (c.type === 'discord') return c.config.webhook_id ? `Webhook ${c.config.webhook_id}` : 'Discord webhook'
  if (c.type === 'browser') return c.config.push_service ? `Browser, through ${c.config.push_service}'s push service` : 'Browser'
  return ''
}

/** The channel of the browser whose push endpoint hashes to hash, if any. */
export function currentBrowserChannel<T extends Pick<NotificationChannel, 'type' | 'config'>>(channels: T[], hash: string | null): T | undefined {
  if (!hash) return undefined
  return channels.find((c) => c.type === 'browser' && c.config.endpoint_hash === hash)
}

/** The bytes of a base64url string (a VAPID public key for applicationServerKey). */
export function base64UrlBytes(s: string): Uint8Array<ArrayBuffer> {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (s.length % 4)) % 4)
  const bin = atob(b64)
  const out = new Uint8Array(new ArrayBuffer(bin.length))
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

/** Lower-case hex of bytes. */
export function hex(buf: ArrayBuffer | Uint8Array): string {
  return Array.from(buf instanceof Uint8Array ? buf : new Uint8Array(buf), (b) => b.toString(16).padStart(2, '0')).join('')
}

export type PushState = 'unsupported' | 'insecure' | 'denied' | 'default' | 'granted'

/** What blocks browser notifications, in one sentence; '' when nothing does. */
export function pushBlockedText(state: PushState, browser = ''): string {
  switch (state) {
    case 'unsupported':
      return /Safari on iOS/.test(browser)
        ? 'On an iPhone or iPad, notifications work after you add bitploy to the Home Screen (Share, Add to Home Screen) and open it from there.'
        : 'This browser cannot show notifications from websites.'
    case 'insecure':
      return 'Browsers only allow notifications on https addresses, and this page is not on one.'
    case 'denied':
      return 'This browser blocks notifications from this site. Allow them in the site settings (the icon left of the address bar), then reload the page.'
    default:
      return ''
  }
}
