import type { AccountOrigin, NewAccountQuota, SignupMode } from './types'

// Who can make an account here and the limits new accounts get (Admin >
// Signups), in the words the pages use.

export const SIGNUP_MODES: { value: SignupMode; label: string; blurb: string }[] = [
  {
    value: 'admin_invites',
    label: 'Only people an admin invites',
    blurb: 'Accounts are made with an invite link from Admin > Invites. A project invite then only works for people who already have an account here.',
  },
  {
    value: 'any_invite',
    label: 'Project invites can also create accounts',
    blurb: 'Admin invite links make accounts, and so does a project invite: someone without an account makes one and joins that project.',
  },
  {
    value: 'open',
    label: 'Anyone can sign up',
    blurb: 'Anyone who can open this server makes an account on /invite/open. The login page links to it.',
  },
]

/** The text of the invite card in a project's Members tab. */
export function projectInviteCardText(signup: boolean): string {
  return signup
    ? 'You get a single-use link that works for 7 days. Send it to them yourself. Someone without an account can sign up with it.'
    : 'You get a single-use link that works for 7 days. Send it to them yourself. Only people who already have an account on this server can accept it.'
}

/** The limits form of Admin > Signups: memory and storage in GB, CPU in cores, like the Limits of a person. */
export interface LimitsForm {
  apps: number
  memory_gb: number
  cpus: number
  domains_per_app: number
  volume_gb: number
}

const toGB = (mb: number) => Math.round((mb / 1024) * 100) / 100
const toMB = (gb: number) => Math.round(gb * 1024)

export function limitsToForm(q: NewAccountQuota): LimitsForm {
  return {
    apps: q.apps,
    memory_gb: toGB(q.memory_mb),
    cpus: q.cpu_millis / 1000,
    domains_per_app: q.domains_per_app,
    volume_gb: toGB(q.volume_mb),
  }
}

export function formToLimits(f: LimitsForm): NewAccountQuota {
  return {
    apps: f.apps,
    memory_mb: toMB(f.memory_gb),
    cpu_millis: Math.round(f.cpus * 1000),
    domains_per_app: f.domains_per_app,
    volume_mb: toMB(f.volume_gb),
  }
}

/**
 * What is wrong with limits against what the server has (0: not known, not
 * checked), by form field. The server checks the same.
 */
export function limitsOverServer(q: NewAccountQuota, server: { memory_mb: number; cpu_millis: number; disk_mb: number }): Partial<Record<keyof LimitsForm, string>> {
  const out: Partial<Record<keyof LimitsForm, string>> = {}
  if (server.memory_mb > 0 && q.memory_mb > server.memory_mb) out.memory_gb = 'More than this server has.'
  if (server.cpu_millis > 0 && q.cpu_millis > server.cpu_millis) out.cpus = 'More than this server has.'
  if (server.disk_mb > 0 && q.volume_mb > server.disk_mb) out.volume_gb = 'More than the disk holds.'
  return out
}

/** How an account was made, for Admin > People; '' when it was not recorded. */
export function joinedText(o: AccountOrigin | null | undefined): string {
  if (!o) return ''
  const by = o.by ? ` from ${o.by.name || o.by.email}` : ''
  switch (o.via) {
    case 'setup':
      return 'Made on the command line'
    case 'admin_invite':
      return `Admin invite${by}`
    case 'open':
      return 'Signed up on their own'
    case 'project_invite': {
      const project = o.project ? ` to ${o.project.name}` : o.project_deleted ? ' to a deleted project' : ''
      return `Project invite${project}${by}`
    }
  }
  return ''
}
