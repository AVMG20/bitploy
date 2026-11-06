import type { App, UpdateAppInput } from './types'

/**
 * The settings of how an app is built and runs that the Settings tab shows
 * next to its type: kind (web or worker), port, health path, Dockerfile path
 * and build folder. They all apply on the next deploy.
 */
export interface RunForm {
  type: 'web' | 'worker'
  /** As typed; empty means "from the Dockerfile's EXPOSE or the image". */
  port: string
  health_path: string
  dockerfile_path: string
  context_dir: string
}

export type RunField = keyof RunForm

type AppLike = Pick<App, 'type' | 'port' | 'health_path' | 'dockerfile_path' | 'context_dir' | 'build_mode' | 'static'>

export function runFormOf(a: AppLike): RunForm {
  return {
    type: a.type,
    port: a.port ? String(a.port) : '',
    health_path: a.health_path || '/',
    dockerfile_path: a.dockerfile_path || 'Dockerfile',
    context_dir: a.context_dir || '.',
  }
}

/**
 * Which fields apply. A site bitploy serves itself (plain website or static
 * build) has bitploy's own port and no worker form. A worker has no port and
 * no health page. The Dockerfile path and build folder only matter when the
 * app builds its own Dockerfile.
 */
export function runFieldsFor(a: AppLike, type: RunForm['type']): Record<RunField, boolean> {
  const site = a.static || a.build_mode === 'static' || a.build_mode === 'static_build'
  const web = type === 'web'
  return {
    type: !site,
    port: web && !site,
    health_path: web && !site,
    dockerfile_path: a.build_mode === 'dockerfile' && !a.static,
    context_dir: a.build_mode === 'dockerfile' && !a.static,
  }
}

export function portError(v: string): string {
  const t = v.trim()
  if (!t) return ''
  if (!/^\d+$/.test(t)) return 'Use a number, like 3000 or 8080.'
  const n = Number(t)
  if (n < 1 || n > 65535) return 'Use a number between 1 and 65535.'
  return ''
}

export function healthPathError(v: string): string {
  const t = v.trim()
  if (!t.startsWith('/')) return 'Start with /, like / or /healthz.'
  if (/\s/.test(t)) return 'Leave out spaces.'
  if (t.length > 200) return 'Use at most 200 characters.'
  return ''
}

/** A path inside the code: relative, without "..". */
export function relPathError(v: string, what: 'file' | 'folder'): string {
  const t = v.trim().replace(/\\/g, '/')
  if (!t) return what === 'file' ? 'Give the path of the Dockerfile, like Dockerfile.' : ''
  if (t.startsWith('/')) return `Use a ${what} inside your code, not an absolute path.`
  if (t.split('/').includes('..')) return `Use a ${what} inside your code, without "..".`
  return ''
}

/** The client-side problems of the fields that apply. */
export function runErrors(f: RunForm, shown: Record<RunField, boolean>): Partial<Record<RunField, string>> {
  const out: Partial<Record<RunField, string>> = {}
  const add = (k: RunField, msg: string) => {
    if (shown[k] && msg) out[k] = msg
  }
  add('port', portError(f.port))
  add('health_path', healthPathError(f.health_path))
  add('dockerfile_path', relPathError(f.dockerfile_path, 'file'))
  add('context_dir', relPathError(f.context_dir, 'folder'))
  return out
}

/** Only what changed, of the fields that apply. An empty port clears it (0). */
export function runChanges(a: AppLike, f: RunForm, shown: Record<RunField, boolean>): UpdateAppInput {
  const base = runFormOf(a)
  const out: UpdateAppInput = {}
  if (shown.type && f.type !== base.type) out.type = f.type
  if (shown.port && f.port.trim() !== base.port) out.port = f.port.trim() ? Number(f.port.trim()) : 0
  if (shown.health_path && f.health_path.trim() !== base.health_path) out.health_path = f.health_path.trim()
  if (shown.dockerfile_path && f.dockerfile_path.trim() !== base.dockerfile_path) out.dockerfile_path = f.dockerfile_path.trim()
  if (shown.context_dir && (f.context_dir.trim() || '.') !== base.context_dir) out.context_dir = f.context_dir.trim() || '.'
  return out
}

/** The field an error of PATCH /apps/{app} is about, so it shows next to it. */
export function runFieldOfError(code: string): RunField | null {
  switch (code) {
    case 'app_type_invalid':
      return 'type'
    case 'port_invalid':
    case 'port_reserved':
      return 'port'
    case 'health_path_invalid':
      return 'health_path'
    case 'dockerfile_path_invalid':
      return 'dockerfile_path'
    case 'context_dir_invalid':
      return 'context_dir'
  }
  return null
}

/** Deploy errors the port field fixes. */
export function isPortError(code: string): boolean {
  return code === 'port_unknown' || code === 'port_ambiguous' || code === 'port_reserved'
}
