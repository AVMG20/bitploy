// The Terminal tab of an app: a shell in its running container over a
// WebSocket (GET /api/v1/apps/{app}/terminal). Binary frames carry what you
// type and what the shell prints; text frames carry one JSON control message.
// This file has the parts without a browser: the address, the messages, the
// state they lead to, and the colours. Only erasable TypeScript, so Node can
// run it in a test.

import { listWords } from './services.ts'
import type { App } from './types'

/** What the browser sends besides keystrokes. */
export interface ResizeMessage {
  type: 'resize'
  cols: number
  rows: number
}

/** What bitploy sends besides the shell's output. */
export type ServerMessage =
  | { type: 'ready'; app: string; service: string }
  | { type: 'exit'; code?: number }
  | { type: 'end'; reason: string; message: string }
  | { type: 'error'; error: { code: string; message: string; hint?: string } }

export type TerminalPhase =
  /** Opening the WebSocket and the shell. */
  | 'connecting'
  /** The shell runs; what you type goes to it. */
  | 'open'
  /** The app has no running container to open a shell in. */
  | 'not_running'
  /** The connection ended: the network, bitploy (idle, too long, access) or the tab. */
  | 'closed'
  /** The shell exited by itself (exit, Ctrl+D). */
  | 'exited'
  /** bitploy refused to open one; message says why. */
  | 'failed'

export interface TerminalState {
  phase: TerminalPhase
  /** Why it closed or failed, in plain words; "" when there is nothing to add. */
  message: string
  hint: string
  /** The shell's exit code, when it exited and Docker knew it. */
  code: number | null
}

export const CONNECTING: TerminalState = { phase: 'connecting', message: '', hint: '', code: null }

export type TerminalEvent = { kind: 'message'; message: ServerMessage } | { kind: 'close' }

/** ws://host/api/v1/apps/<slug>/terminal?cols=&rows=&service= for the page at loc. */
export function terminalUrl(loc: { protocol: string; host: string }, slug: string, o: { cols: number; rows: number; service?: string }): string {
  const q = new URLSearchParams({ cols: String(o.cols), rows: String(o.rows) })
  if (o.service) q.set('service', o.service)
  const scheme = loc.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${scheme}//${loc.host}/api/v1/apps/${encodeURIComponent(slug)}/terminal?${q.toString()}`
}

export function resizeMessage(cols: number, rows: number): string {
  const m: ResizeMessage = { type: 'resize', cols, rows }
  return JSON.stringify(m)
}

/** One control message from bitploy; null for anything this dashboard does not know. */
export function parseServerMessage(text: string): ServerMessage | null {
  let v: unknown
  try {
    v = JSON.parse(text)
  } catch {
    return null
  }
  if (!v || typeof v !== 'object') return null
  const m = v as Record<string, unknown>
  const str = (x: unknown) => (typeof x === 'string' ? x : '')
  switch (m.type) {
    case 'ready':
      return { type: 'ready', app: str(m.app), service: str(m.service) }
    case 'exit':
      return typeof m.code === 'number' ? { type: 'exit', code: m.code } : { type: 'exit' }
    case 'end':
      return { type: 'end', reason: str(m.reason), message: str(m.message) }
    case 'error': {
      const e = (m.error && typeof m.error === 'object' ? m.error : {}) as Record<string, unknown>
      const hint = str(e.hint)
      return { type: 'error', error: { code: str(e.code) || 'terminal_failed', message: str(e.message) || 'The terminal could not open.', ...(hint ? { hint } : {}) } }
    }
  }
  return null
}

/** The state after an event. The first ending wins: the close that follows an exit or an error changes nothing. */
export function nextTerminalState(s: TerminalState, e: TerminalEvent): TerminalState {
  const done = s.phase !== 'connecting' && s.phase !== 'open'
  if (e.kind === 'close') {
    return done ? s : { phase: 'closed', message: s.phase === 'connecting' ? 'The terminal could not connect.' : 'The connection to the terminal was lost.', hint: '', code: null }
  }
  if (done) return s
  const m = e.message
  switch (m.type) {
    case 'ready':
      return { phase: 'open', message: '', hint: '', code: null }
    case 'exit':
      return { phase: 'exited', message: '', hint: '', code: m.code ?? null }
    case 'end':
      return { phase: m.reason === 'not_running' ? 'not_running' : 'closed', message: m.message, hint: '', code: null }
    case 'error':
      if (m.error.code === 'app_not_running') return { phase: 'not_running', message: m.error.message, hint: m.error.hint ?? '', code: null }
      return { phase: 'failed', message: m.error.message, hint: m.error.hint ?? '', code: null }
  }
  return s
}

/** The line next to the terminal. */
export function terminalStatusText(s: TerminalState): string {
  switch (s.phase) {
    case 'connecting':
      return 'Connecting...'
    case 'open':
      return 'Connected'
    case 'not_running':
      return 'Not running'
    case 'closed':
      return s.message || 'Disconnected'
    case 'exited':
      return s.code === null ? 'The shell exited.' : `The shell exited with code ${s.code}.`
    case 'failed':
      return s.message
  }
}

/**
 * The line above the terminal: what survives a deploy. Only the volumes the
 * service mounts do (/data of a normal app); service "" is the app's one.
 */
export function terminalAbout(app: Pick<App, 'volume' | 'volumes'> | null | undefined, service: string): string {
  const paths = (app?.volumes ?? []).filter((v) => !service || v.service === service).map((v) => v.mount_path)
  if (!paths.length && app?.volume && !service) paths.push('/data')
  const kept = paths.length ? `Changes to files outside ${listWords(paths)} are lost on the next deploy.` : 'Changes to files are lost on the next deploy.'
  return `Runs a shell inside the running container. ${kept}`
}

/**
 * Whether the dashboard offers a terminal for app. bitploy opens one for the
 * app's owner and the members of its project, never for a static site (no
 * container of its own); an admin looking at someone else's app gets none.
 * userId "" (the session is still loading) leaves the decision to the server.
 */
export function terminalOffered(app: Pick<App, 'static' | 'owner_id' | 'shared'> | null | undefined, userId: string): boolean {
  if (!app || app.static) return false
  return !userId || app.owner_id === userId || !!app.shared
}

/**
 * The terminal's colours: the dashboard's dark tokens (style.css) as hex,
 * because xterm.js does not read oklch. It stays dark in the light theme too.
 * No blue: ANSI blue is a violet and cyan a teal, so `ls` colours stay
 * readable without leaving the palette.
 */
export const TERMINAL_THEME = {
  background: '#0d0d0d',
  foreground: '#e8e8e8',
  cursor: '#f5642b', // --primary
  cursorAccent: '#0d0d0d',
  selectionBackground: 'rgba(245, 100, 43, 0.3)',
  black: '#262626',
  red: '#f05653', // --destructive
  green: '#4dbf74', // --success
  yellow: '#efb146', // --warning
  blue: '#bd84d6',
  magenta: '#e96fa3', // --chart-5
  cyan: '#55bfb3', // --chart-2
  white: '#d1d1d1',
  brightBlack: '#636363',
  brightRed: '#ff7e76',
  brightGreen: '#78d694',
  brightYellow: '#f8ce73',
  brightBlue: '#d2a2e8',
  brightMagenta: '#f893bc',
  brightCyan: '#7cd7cc',
  brightWhite: '#f5f5f5',
}
