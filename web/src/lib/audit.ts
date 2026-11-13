// The activity log in words: "Changed an app's settings" instead of app.update.
// Only erasable TypeScript, so Node can run the file in a test (tests/audit.test.ts).

import { channelLabel } from './format.ts'
import type { ActivityEntry } from './types'

export type AuditGroup = 'apps' | 'deploys' | 'data' | 'domains' | 'access' | 'github' | 'admin'

export const AUDIT_GROUPS: { value: AuditGroup; label: string }[] = [
  { value: 'apps', label: 'Apps' },
  { value: 'deploys', label: 'Deploys' },
  { value: 'data', label: 'Files and backups' },
  { value: 'domains', label: 'Domains' },
  { value: 'access', label: 'Logins and tokens' },
  { value: 'github', label: 'GitHub' },
  { value: 'admin', label: 'Admin' },
]

const LABELS: Record<string, string> = {
  'app.create': 'Created an app',
  'app.update': 'Changed app settings',
  'app.delete': 'Deleted an app',
  'app.start': 'Started an app',
  'app.stop': 'Stopped an app',
  'app.restart': 'Restarted an app',
  'app.terminal_open': 'Opened a terminal',
  'app.terminal_close': 'Closed a terminal',
  'app.restore': 'Restored an app',
  'app.crashed': 'App crashed',
  'app.container_died': 'App stopped by itself',
  'app.oom': 'App ran out of memory',
  'app.healthy': 'App is healthy again',
  'app.unhealthy': 'App stopped answering',
  'app.volume_quota_exceeded': 'Saved files went over their limit',
  'app.disk_stopped': 'Stopped: it filled the server disk',
  'app.protection_set': 'Changed password protection',
  'app.backup_settings': 'Changed automatic backups',
  'app.git_source_set': 'Linked a repository',
  'app.git_source_clear': 'Unlinked a repository',
  'app.git_auto_deploy': 'Changed deploy on push',
  'env.set': 'Changed environment variables',
  'dockerfile.update': 'Edited the Dockerfile',
  'catalog.deploy': 'Added a ready-made app',
  'catalog.update': 'Updated a ready-made app',
  'deployment.queue': 'Started a deploy',
  'deployment.running': 'Deploy went live',
  'deployment.failed': 'Deploy failed',
  'deployment.cancelled': 'Cancelled a deploy',
  'deployment.interrupted': 'Deploy was interrupted',
  'upload.create': 'Started an upload',
  'github.push_not_deployed': 'A push was not deployed',
  'backup.create': 'Made a backup',
  'backup.delete': 'Deleted a backup',
  'backup.download': 'Downloaded a backup',
  'backup.upload': 'Uploaded a backup',
  'backup.restore': 'Restored a backup',
  'backup.restore_failed': 'Restoring a backup failed',
  'backup.restore_recovered': 'Recovered from a failed restore',
  'files.upload': 'Uploaded saved files',
  'files.download': 'Downloaded a saved file',
  'files.delete': 'Deleted saved files',
  'files.mkdir': 'Made a folder in saved files',
  'db.write': 'Changed data in a database',
  'domain.add': 'Added a domain',
  'domain.remove': 'Removed a domain',
  'domain.claim_replaced': 'A domain moved to another app',
  'auth.login': 'Logged in',
  'auth.login_failed': 'Login failed',
  'auth.logout': 'Logged out',
  'session.revoke': 'Logged out a device',
  'session.revoke_others': 'Logged out all other devices',
  'token.create': 'Made an API token',
  'token.revoke': 'Revoked an API token',
  'user.update': 'Changed a profile',
  'user.email_change': 'Changed an email address',
  'user.password_change': 'Changed a password',
  'invite.create': 'Made an invite',
  'invite.revoke': 'Revoked an invite',
  'admin.quota_set': 'Changed someone’s limits',
  'admin.settings_update': 'Changed who can sign up or the limits for new accounts',
  'admin.project_invite_revoke': 'Revoked someone’s project invite',
  'user.signup': 'Made an account',
  'admin.db.query': 'Ran a query on the bitploy database',
  'admin.disk_cleanup': 'Cleaned up disk space',
  'admin.disk_cleanup_settings': 'Changed the automatic disk cleanup',
  'server.disk_cleanup': 'bitploy cleaned up disk space',
  'server.builds_paused': 'Builds paused: the disk is almost full',
  'server.builds_resumed': 'Builds run again: the disk has room',
  'github_app.create': 'Set up the GitHub app',
  'github_app.disconnect': 'Disconnected the GitHub app',
  'github.installation_link': 'Connected a GitHub account',
  'github.installation_unlink': 'Disconnected a GitHub account',
  'app.project': 'Changed an app’s short name',
  'app.git_repo_renamed': 'The repository was renamed',
  'app.public_port_opened': 'Opened a public port',
  'app.public_port_changed': 'Changed a public port',
  'app.public_port_closed': 'Closed a public port',
  'app.public_port_moved': 'A public port moved',
  'app.public_port_revealed': 'Showed a public port',
  'upload.receive': 'Received an upload',
  'service.add': 'Added a service',
  'domain.active': 'A domain is live',
  'domain.failed': 'A domain could not be set up',
  'project.create': 'Made a project',
  'project.update': 'Renamed a project',
  'project.delete': 'Deleted a project',
  'project.invite_create': 'Invited someone to a project',
  'project.invite_revoke': 'Revoked a project invite',
  'project.member_add': 'Joined a project',
  'project.member_remove': 'Removed someone from a project',
  'project.leave': 'Left a project',
  'notification_channel.create': 'Added a notification channel',
  'notification_channel.update': 'Changed a notification channel',
  'notification_channel.delete': 'Removed a notification channel',
  'notification_channel.disable': 'Turned off a notification channel',
  'user.create': 'Made an account',
  'user.password_reset': 'Set a new password with a reset link',
  'admin.user_suspend': 'Suspended an account',
  'admin.user_reactivate': 'Reactivated an account',
  'admin.password_reset_link': 'Made a password reset link',
  'admin.user_app_domains': 'Changed someone’s domains',
}

/** "Changed app settings" for app.update; unknown actions in plain words from their code. */
export function auditLabel(action: string): string {
  if (LABELS[action]) return LABELS[action]
  const words = action.replace(/[._]/g, ' ').trim()
  return words ? words[0].toUpperCase() + words.slice(1) : action
}

export function auditGroup(action: string): AuditGroup {
  const [head] = action.split('.')
  if (action.startsWith('admin.') || head === 'invite' || head === 'user' || head === 'server') return 'admin'
  if (head === 'deployment' || head === 'upload' || head === 'catalog' || head === 'dockerfile' || action === 'github.push_not_deployed') return 'deploys'
  if (head === 'backup' || head === 'files' || head === 'db') return 'data'
  if (head === 'domain') return 'domains'
  if (head === 'auth' || head === 'session' || head === 'token') return 'access'
  if (head === 'github' || head === 'github_app' || action.startsWith('app.git_')) return 'github'
  return 'apps'
}

const ROLE_WORDS: Record<string, string> = { user: 'Member', admin: 'Admin' }

const isScalar = (x: unknown) => x === null || ['string', 'number', 'boolean'].includes(typeof x)

/**
 * A change logged as {from, to}: "a → b", or for two objects (the limits for
 * new accounts) only the keys that changed, "apps 10 → 2".
 */
function fromTo(v: unknown): string | null {
  if (!v || typeof v !== 'object' || Array.isArray(v) || !('from' in v) || !('to' in v)) return null
  const { from, to } = v as { from: unknown; to: unknown }
  if (isScalar(from) && isScalar(to)) return `${String(from)} → ${String(to)}`
  if (from && to && typeof from === 'object' && typeof to === 'object') {
    const a = from as Record<string, unknown>
    const b = to as Record<string, unknown>
    const parts = Object.keys(b)
      .filter((k) => a[k] !== b[k])
      .map((k) => `${k.replace(/_/g, ' ')} ${String(a[k])} → ${String(b[k])}`)
    return parts.join(', ')
  }
  return null
}

/** The details of an entry as short "key: value" parts, without ids nobody can read. */
export function auditDetails(d: Record<string, unknown> | null): string[] {
  if (!d) return []
  const out: string[] = []
  for (const [k, v] of Object.entries(d)) {
    if (v === null || v === undefined || v === '' || /(^|_)id$/.test(k)) continue
    // A list of names or numbers reads as a list; other objects stay JSON.
    const plain = Array.isArray(v) && v.length > 0 && v.every((x) => typeof x === 'string' || typeof x === 'number')
    const change = fromTo(v)
    let val = change ?? (plain ? (v as unknown[]).join(', ') : typeof v === 'object' ? JSON.stringify(v) : String(v))
    // Roles in the words the invite form uses.
    if (k === 'role') val = ROLE_WORDS[val] ?? val
    out.push(`${k.replace(/_/g, ' ')}: ${val.length > 80 ? `${val.slice(0, 77)}...` : val}`)
  }
  return out
}

// ---------------------------------------------------------------- rows

/**
 * One row of an activity list, the same on Admin > Activity log and on the
 * Activity page (components/ActivityLog.vue); each page makes its rows from
 * what its endpoint returns.
 */
export interface AuditRow {
  id: number
  ts: string
  /** Who did it, in words: a name, "You", "An admin", "bitploy". */
  who: string
  channel: string
  action: string
  /** What happened, when it reads better than auditLabel(action) for this viewer. */
  label?: string
  /** What it is about, the page for it when the viewer may open it, and where it is (an app's project). */
  target: { text: string; to?: string; sub?: string } | null
  details: string[]
  /** Where it came from; empty when the viewer may not see it. */
  ip: string
}

/** The rows of one kind that hold every word of the search, anywhere. */
export function filterAuditRows(rows: AuditRow[], kind: string, search: string): AuditRow[] {
  const words = search.trim().toLowerCase().split(/\s+/).filter(Boolean)
  return rows.filter((r) => {
    if (kind !== 'all' && auditGroup(r.action) !== kind) return false
    if (!words.length) return true
    const hay = [r.label ?? auditLabel(r.action), r.action, r.who, r.target?.text ?? '', r.target?.sub ?? '', channelLabel(r.channel), r.ip, ...r.details]
      .join(' ')
      .toLowerCase()
    return words.every((w) => hay.includes(w))
  })
}

// What an admin did to the viewer's own account, said to them.
const YOUR_ACCOUNT: Record<string, string> = {
  'admin.quota_set': 'Changed your limits',
  'admin.user_suspend': 'Suspended your account',
  'admin.user_reactivate': 'Reactivated your account',
  'admin.password_reset_link': 'Made a password reset link for you',
  'admin.user_app_domains': 'Changed your domains',
}

/** An entry of GET /activity as a row: the server already says who, and which app. */
export function activityRow(e: ActivityEntry): AuditRow {
  let target: AuditRow['target'] = null
  if (e.app) {
    const text = e.app.name || e.app.slug || 'a deleted app'
    target = e.app.gone || !e.app.slug ? { text } : { text, to: `/apps/${encodeURIComponent(e.app.slug)}` }
    if (e.project?.name) target.sub = e.project.name
  } else if (e.project) {
    target = e.project.gone || !e.project.id ? { text: e.project.name } : { text: e.project.name, to: `/projects/${encodeURIComponent(e.project.id)}` }
  } else if (e.target_type === 'domain' && typeof e.details?.hostname === 'string') {
    target = { text: e.details.hostname }
  }
  return {
    id: e.id,
    ts: e.ts,
    who: e.actor_name,
    channel: e.channel,
    action: e.action,
    label: e.target_type === 'user' ? YOUR_ACCOUNT[e.action] : undefined,
    target,
    details: auditDetails(e.details),
    ip: e.ip ?? '',
  }
}
