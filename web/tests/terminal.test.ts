import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  CONNECTING,
  inputFrames,
  nextTerminalState,
  parseServerMessage,
  resizeMessage,
  TERMINAL_THEME,
  terminalAbout,
  terminalOffered,
  terminalStatusText,
  terminalUrl,
} from '../src/lib/terminal.ts'
import type { ServerMessage, TerminalState } from '../src/lib/terminal.ts'

test('terminalUrl speaks wss on https and names the service', () => {
  assert.equal(
    terminalUrl({ protocol: 'https:', host: 'bitploy.example.com' }, 'shop', { cols: 120, rows: 40 }),
    'wss://bitploy.example.com/api/v1/apps/shop/terminal?cols=120&rows=40',
  )
  assert.equal(
    terminalUrl({ protocol: 'http:', host: 'localhost:5173' }, 'shop', { cols: 80, rows: 24, service: 'worker' }),
    'ws://localhost:5173/api/v1/apps/shop/terminal?cols=80&rows=24&service=worker',
  )
})

test('resizeMessage is the JSON bitploy reads', () => {
  assert.deepEqual(JSON.parse(resizeMessage(100, 30)), { type: 'resize', cols: 100, rows: 30 })
})

test('parseServerMessage reads the control messages and drops the rest', () => {
  assert.deepEqual(parseServerMessage('{"type":"ready","app":"shop","service":""}'), { type: 'ready', app: 'shop', service: '' })
  assert.deepEqual(parseServerMessage('{"type":"exit","code":3}'), { type: 'exit', code: 3 })
  assert.deepEqual(parseServerMessage('{"type":"exit"}'), { type: 'exit' })
  assert.deepEqual(parseServerMessage('{"type":"end","reason":"idle","message":"Idle."}'), { type: 'end', reason: 'idle', message: 'Idle.' })
  assert.deepEqual(parseServerMessage('{"type":"error","error":{"code":"too_many_terminals","message":"Limit.","hint":"Close one."}}'), {
    type: 'error',
    error: { code: 'too_many_terminals', message: 'Limit.', hint: 'Close one.' },
  })
  assert.deepEqual(parseServerMessage('{"type":"error"}'), { type: 'error', error: { code: 'terminal_failed', message: 'The terminal could not open.' } })
  assert.equal(parseServerMessage('{"type":"later"}'), null)
  assert.equal(parseServerMessage('not json'), null)
  assert.equal(parseServerMessage('null'), null)
})

function run(...events: (ServerMessage | 'close')[]): TerminalState {
  return events.reduce<TerminalState>((s, e) => nextTerminalState(s, e === 'close' ? { kind: 'close' } : { kind: 'message', message: e }), CONNECTING)
}

test('the session states follow the messages, and the first ending wins', () => {
  assert.equal(run({ type: 'ready', app: 'shop', service: '' }).phase, 'open')

  const exited = run({ type: 'ready', app: 'shop', service: '' }, { type: 'exit', code: 3 }, 'close')
  assert.deepEqual(exited, { phase: 'exited', message: '', hint: '', code: 3 })
  assert.equal(terminalStatusText(exited), 'The shell exited with code 3.')
  assert.equal(terminalStatusText(run({ type: 'ready', app: 'shop', service: '' }, { type: 'exit' })), 'The shell exited.')
  assert.equal(terminalStatusText(run({ type: 'ready', app: 'shop', service: '' }, { type: 'exit', code: 0 })), 'The shell exited.')

  const idle = run({ type: 'ready', app: 'shop', service: '' }, { type: 'end', reason: 'idle', message: 'Closed after 30 minutes.' }, 'close')
  assert.equal(idle.phase, 'closed')
  assert.equal(terminalStatusText(idle), 'Closed after 30 minutes.')
  assert.equal(run({ type: 'ready', app: 'shop', service: '' }, { type: 'end', reason: 'not_running', message: 'Stopped.' }).phase, 'not_running')

  const lost = run({ type: 'ready', app: 'shop', service: '' }, 'close')
  assert.equal(lost.phase, 'closed')
  assert.equal(lost.message, 'The connection to the terminal was lost.')
  assert.equal(run('close').message, 'The terminal could not connect.')
})

test('errors before the shell runs: not running, or why it failed', () => {
  const notRunning = run({ type: 'error', error: { code: 'app_not_running', message: 'The app is not running.', hint: 'Start it.' } }, 'close')
  assert.deepEqual(notRunning, { phase: 'not_running', message: 'The app is not running.', hint: 'Start it.', code: null })
  const tooMany = run({ type: 'error', error: { code: 'too_many_terminals', message: 'Limit reached.' } }, 'close')
  assert.equal(tooMany.phase, 'failed')
  assert.equal(terminalStatusText(tooMany), 'Limit reached.')
})

test('terminalOffered: owners and members, no static sites, no admin on another user’s app', () => {
  const app = { static: false, owner_id: 'usr_a', shared: false }
  assert.equal(terminalOffered(app, 'usr_a'), true)
  assert.equal(terminalOffered({ ...app, shared: true }, 'usr_b'), true)
  assert.equal(terminalOffered(app, 'usr_admin'), false)
  assert.equal(terminalOffered({ ...app, static: true }, 'usr_a'), false)
  assert.equal(terminalOffered(app, ''), true)
  assert.equal(terminalOffered(null, 'usr_a'), false)
})

test('terminalAbout names the folders that survive a deploy', () => {
  const vol = (service: string, mount_path: string) => ({ name: 'v', service, mount_path, size_mb: 1, used_mb: null })
  assert.equal(
    terminalAbout({ volume: true, volumes: [vol('app', '/data')] }, ''),
    'Runs a shell inside the running container. Changes to files outside /data are lost on the next deploy.',
  )
  assert.equal(terminalAbout({ volume: true, volumes: [] }, ''), 'Runs a shell inside the running container. Changes to files outside /data are lost on the next deploy.')
  assert.equal(terminalAbout({ volume: false, volumes: [] }, ''), 'Runs a shell inside the running container. Changes to files are lost on the next deploy.')
  const multi = { volume: false, volumes: [vol('db', '/var/lib/postgresql/data'), vol('web', '/uploads'), vol('web', '/cache')] }
  assert.match(terminalAbout(multi, 'web'), /outside \/uploads and \/cache are lost/)
  assert.match(terminalAbout(multi, 'worker'), /Changes to files are lost/)
})

test('the terminal colours are hex and hold no blue', () => {
  for (const [name, c] of Object.entries(TERMINAL_THEME)) {
    const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/.exec(c)
    if (!m) {
      assert.match(c, /^rgba\(/, name)
      continue
    }
    const [r, g, b] = m.slice(1).map((h) => parseInt(h, 16)) as [number, number, number]
    // Blue as the strongest channel by far reads as blue; violet keeps red close.
    assert.ok(!(b > r + 40 && b > g + 40), `${name} ${c} is blue`)
  }
})

test('inputFrames keeps a large paste under the 64 KB frame limit', () => {
  const small = new Uint8Array(10)
  assert.deepEqual(inputFrames(small), [small])
  const big = new Uint8Array(100_000).map((_, i) => i & 0xff)
  const frames = inputFrames(big)
  assert.ok(frames.every((f) => f.length <= 16 << 10))
  assert.deepEqual(Buffer.concat(frames), Buffer.from(big))
})
