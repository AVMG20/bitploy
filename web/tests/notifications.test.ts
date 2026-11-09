import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  base64UrlBytes,
  browserName,
  channelHealth,
  channelTarget,
  currentBrowserChannel,
  defaultEvents,
  discordWebhookProblem,
  emergencyMark,
  groupEvents,
  hex,
  pushBlockedText,
  toggleEvent,
} from '../src/lib/notifications.ts'
import type { NotificationEvent } from '../src/lib/types.ts'

const catalog: NotificationEvent[] = [
  { type: 'deployment.started', group: 'deployments', label: 'Started', emergency: false, default: false },
  { type: 'deployment.failed', group: 'deployments', label: 'Failed', emergency: true, default: true },
  { type: 'app.crashed', group: 'app', label: 'Crashed', emergency: true, default: true },
  { type: 'backup.failed', group: 'backups', label: 'Backup failed', emergency: true, default: true },
]

test('events are grouped Deployments, Backups, App', () => {
  const groups = groupEvents(catalog)
  assert.deepEqual(
    groups.map((g) => [g.label, g.events.map((e) => e.type)]),
    [
      ['Deployments', ['deployment.started', 'deployment.failed']],
      ['Backups', ['backup.failed']],
      ['App', ['app.crashed']],
    ],
  )
  assert.deepEqual(groupEvents(catalog.slice(0, 2)).map((g) => g.group), ['deployments'])
})

test('defaults and toggling keep the catalog order', () => {
  assert.deepEqual(defaultEvents(catalog), ['deployment.failed', 'app.crashed', 'backup.failed'])
  assert.deepEqual(toggleEvent(['app.crashed'], 'deployment.started', true, catalog), ['deployment.started', 'app.crashed'])
  assert.deepEqual(toggleEvent(['app.crashed', 'deployment.failed'], 'app.crashed', false, catalog), ['deployment.failed'])
  assert.deepEqual(toggleEvent(['app.crashed'], 'app.crashed', true, catalog), ['app.crashed'])
})

// The same rules as TestParseDiscordWebhook in internal/notify/discord_test.go.
test('Discord webhook URL check', () => {
  const token = 'abcdefghijklmnopqrstuvwxyz_-0123456789'
  for (const ok of [
    `https://discord.com/api/webhooks/123456789012345678/${token}`,
    `https://discordapp.com/api/webhooks/123456789012345678/${token}`,
    `https://canary.discord.com/api/webhooks/123456789012345678/${token}`,
    `https://ptb.discord.com/api/webhooks/123456789012345678/${token}`,
    `  https://DISCORD.com/api/webhooks/123456789012345678/${token}  `,
  ]) {
    assert.equal(discordWebhookProblem(ok), '', ok)
  }
  for (const bad of [
    '',
    'discord webhook',
    `http://discord.com/api/webhooks/123456789012345678/${token}`,
    `https://discord.com.evil.example/api/webhooks/123456789012345678/${token}`,
    `https://evil.example/api/webhooks/123456789012345678/${token}`,
    `https://discord.com:8443/api/webhooks/123456789012345678/${token}`,
    `https://user@discord.com/api/webhooks/123456789012345678/${token}`,
    `https://discord.com/api/webhooks/123456789012345678/${token}?wait=true`,
    `https://discord.com/api/webhooks/123456789012345678`,
    `https://discord.com/api/webhooks/abc/${token}`,
    `https://discord.com/api/v10/webhooks/123456789012345678/${token}`,
  ]) {
    assert.notEqual(discordWebhookProblem(bad), '', bad)
  }
})

test('channel health follows the newest outcome', () => {
  assert.equal(channelHealth({ last_success_at: null, last_error: '', last_error_at: null }), 'unused')
  assert.equal(channelHealth({ last_success_at: '2026-11-08T10:00:00Z', last_error: '', last_error_at: null }), 'ok')
  assert.equal(channelHealth({ last_success_at: null, last_error: 'Unknown webhook', last_error_at: '2026-11-08T10:00:00Z' }), 'failing')
  assert.equal(
    channelHealth({ last_success_at: '2026-11-08T09:00:00Z', last_error: 'Unknown webhook', last_error_at: '2026-11-08T10:00:00Z' }),
    'failing',
  )
  assert.equal(
    channelHealth({ last_success_at: '2026-11-08T11:00:00Z', last_error: 'Unknown webhook', last_error_at: '2026-11-08T10:00:00Z' }),
    'ok',
  )
})

test('browser names', () => {
  const cases: Record<string, string> = {
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36': 'Chrome on macOS',
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0': 'Edge on Windows',
    'Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0': 'Firefox on Linux',
    'Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1': 'Safari on iOS',
    'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36': 'Chrome on Android',
    'curl/8.0': 'Browser',
  }
  for (const [ua, want] of Object.entries(cases)) assert.equal(browserName(ua), want, ua)
})

test('emergency marks per channel type', () => {
  assert.equal(emergencyMark('discord'), 'pings @everyone')
  assert.equal(emergencyMark('browser'), 'stays on screen')
})

test('channel target lines show no secrets', () => {
  assert.equal(channelTarget({ type: 'discord', config: { webhook_id: '1122', webhook_url_set: true } }), 'Webhook 1122')
  assert.equal(channelTarget({ type: 'browser', config: { push_service: 'Google', endpoint_hash: 'ab' } }), "Browser, through Google's push service")
})

test('the channel of this browser is found by endpoint hash', () => {
  const list = [
    { id: 'a', type: 'discord' as const, config: { endpoint_hash: 'x' } },
    { id: 'b', type: 'browser' as const, config: { endpoint_hash: 'y' } },
    { id: 'c', type: 'browser' as const, config: { endpoint_hash: 'x' } },
  ]
  assert.equal(currentBrowserChannel(list, 'x')?.id, 'c')
  assert.equal(currentBrowserChannel(list, 'z'), undefined)
  assert.equal(currentBrowserChannel(list, null), undefined)
})

test('VAPID keys decode from base64url', () => {
  // 0xfb 0xff 0xbf: "+/+/" in base64, "-_-_" in base64url
  assert.deepEqual([...base64UrlBytes('-_-_')], [0xfb, 0xff, 0xbf])
  assert.deepEqual([...base64UrlBytes('AQ')], [1])
  assert.equal(base64UrlBytes('BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4').length, 65)
  assert.equal(hex(new Uint8Array([0, 15, 255])), '000fff')
})

test('blocked browser notifications get one plain sentence', () => {
  assert.equal(pushBlockedText('granted'), '')
  assert.equal(pushBlockedText('default'), '')
  assert.match(pushBlockedText('denied'), /site settings/)
  assert.match(pushBlockedText('insecure'), /https/)
  assert.match(pushBlockedText('unsupported', 'Safari on iOS'), /Home Screen/)
  assert.match(pushBlockedText('unsupported', 'Firefox on Linux'), /cannot show notifications/)
  for (const s of ['denied', 'insecure', 'unsupported'] as const) {
    assert.doesNotMatch(pushBlockedText(s), /simply|just|don't worry|as usual/i)
  }
})
