import { api, fetchLogLines } from './api'
import { describeSource, shortSha } from './format'
import type { App, Deployment } from './types'

// "Copy for AI": a short text about a failure to paste into an AI assistant.
// It names the app and deployment the way bitploy's tools do (slug, dep_ id),
// says what failed, and ends with the log tail. It never fetches environment
// variables, so no values from there end up in it.

/** How many of the last log lines go into the text. */
const LOG_LINES = 70
const MAX_LINE = 400

/** A log tail; lines is null when the log could not be loaded. */
type Log = { title: string; lines: string[] | null }

/** What went wrong with a deployment, in a few words. */
function deploymentFailure(d: Deployment): string {
  switch (d.error?.code) {
    case 'build_failed':
      return 'the build failed'
    case 'unhealthy':
      return 'the app started but failed the health check'
    case 'app_crashed':
      return 'the app crashed right after starting'
    case 'out_of_memory':
      return 'the app ran out of memory'
    case 'start_failed':
      return 'the container could not start'
    default:
      return 'the deployment failed'
  }
}

function sourceLine(d: Deployment, app: App): string {
  if (d.source_kind !== 'git') return describeSource(d, app)
  const gs = app.git_source
  const repo = gs ? (gs.repo ?? gs.repo_url.replace(/^https?:\/\//, '').replace(/\.git$/, '')) : 'a git repository'
  const at = d.commit_sha ? `@${shortSha(d.commit_sha)}` : ''
  const branch = gs ? `, branch ${gs.branch}` : ''
  const msg = d.commit_message ? `: "${d.commit_message.split('\n')[0]}"` : ''
  return `git ${repo}${at}${branch}${msg}`
}

/** A code fence that the log itself cannot close. */
function fenced(lines: string[]): string {
  const body = lines.join('\n')
  const longest = Math.max(0, ...(body.match(/`+/g) ?? []).map((m) => m.length))
  const fence = '`'.repeat(Math.max(3, longest + 1))
  return `${fence}\n${body}\n${fence}`
}

function tidy(lines: string[]): string[] {
  return lines.slice(-LOG_LINES).map((l) => (l.length > MAX_LINE ? l.slice(0, MAX_LINE) + ' ...' : l))
}

/** The deployment log holds the build, the start and, on failure, the app's own output. */
async function deploymentLog(d: Deployment): Promise<Log> {
  const title = d.error?.code === 'build_failed' ? 'Build log' : 'Deployment log'
  try {
    const lines = await fetchLogLines({ slug: d.app_slug, kind: 'build', deploymentId: d.id })
    if (lines.length) return { title, lines: tidy(lines) }
  } catch {
    // fall back to the short tail kept with the deployment
  }
  return { title, lines: d.log_tail?.length ? tidy(d.log_tail) : null }
}

async function runtimeLog(app: App): Promise<Log> {
  try {
    return { title: 'App log', lines: tidy(await fetchLogLines({ slug: app.slug, kind: 'runtime' })) }
  } catch {
    return { title: 'App log', lines: null } // the text still helps without the log
  }
}

function appLine(app: App): string {
  return `App: ${app.slug} ("${app.name}")${app.type === 'web' && app.url ? `, ${app.url}` : ', background worker'}`
}

function logBlock(log: Log): string[] {
  if (!log.lines) return [`The ${log.title.toLowerCase()} could not be loaded. Read it with bitploy's get_logs tool.`]
  if (!log.lines.length) return [`The ${log.title.toLowerCase()} is empty.`]
  return [`${log.title}, last ${log.lines.length} lines:`, fenced(log.lines)]
}

function failedDeploymentText(app: App, d: Deployment, log: Log): string {
  const lines = [
    'My app on bitploy failed to deploy. Please find the cause, fix it, and deploy again.',
    '',
    appLine(app),
    `Deployment: ${d.id} (#${d.seq}), ${deploymentFailure(d)}`,
    `Source: ${sourceLine(d, app)}`,
  ]
  if (d.error?.message) lines.push(`Error: ${d.error.message}`)
  if (d.error?.hint) lines.push(`Hint from bitploy: ${d.error.hint}`)
  lines.push('', ...logBlock(log))
  return lines.join('\n')
}

function crashText(app: App, dep: Deployment | null, log: Log): string {
  const what = app.status === 'crashed' ? 'keeps crashing' : 'stopped working'
  const lines = [`My app on bitploy ${what}. Please find the cause, fix it, and deploy again.`, '', appLine(app)]
  if (app.status_message) lines.push(`Status: ${app.status_message}`)
  if (app.restart_count > 0) lines.push(`Restarts: ${app.restart_count}`)
  if (dep) lines.push(`Current deployment: ${dep.id} (#${dep.seq})`, `Source: ${sourceLine(dep, app)}`)
  lines.push('', ...logBlock(log))
  return lines.join('\n')
}

/**
 * The text to paste into an AI assistant when something failed. Never rejects:
 * if the log cannot be loaded the text says so.
 *
 * With a deployment it describes that failed deployment. Without one it
 * describes the app: the newest deployment if that is the one that failed
 * (an app whose last deploy failed), otherwise the crash of the running version.
 */
export async function buildFailureText(app: App, deployment?: Deployment | null): Promise<string> {
  let d = deployment ?? null
  if (!d && app.status === 'failed') {
    try {
      const newest = (await api.deployments(app.slug))[0]
      if (newest?.state === 'failed' && newest.id !== app.current_deployment?.id) d = newest
    } catch {
      d = null
    }
  }
  if (d) return failedDeploymentText(app, d, await deploymentLog(d))
  return crashText(app, app.current_deployment, await runtimeLog(app))
}
