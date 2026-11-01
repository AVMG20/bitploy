import { api, fetchLogLines } from './api'
import { describeSource, shortSha } from './format'
import type { App, Deployment } from './types'

/** How many of the last log lines go into the text. */
const LOG_LINES = 70
const MAX_LINE = 400

/** What went wrong with a deployment, in a few plain words. */
function deploymentFailure(d: Deployment): string {
  switch (d.error?.code) {
    case 'build_failed':
      return 'The build failed'
    case 'unhealthy':
      return 'The health check failed: the app started but did not answer in time'
    case 'app_crashed':
      return 'The app crashed right after it started'
    case 'out_of_memory':
      return 'The app ran out of memory'
    case 'start_failed':
      return 'The app could not start'
    default:
      return 'The deployment failed'
  }
}

function sourceLine(d: Deployment, app: App): string {
  if (d.source_kind === 'git') {
    const gs = app.git_source
    const repo = gs ? (gs.repo ?? gs.repo_url.replace(/^https?:\/\//, '').replace(/\.git$/, '')) : 'a git repository'
    const at = d.commit_sha ? `@${shortSha(d.commit_sha)}` : ''
    const msg = d.commit_message ? ` "${d.commit_message.split('\n')[0]}"` : ''
    return `git ${repo}${at}${gs ? ` (branch ${gs.branch})` : ''}${msg}`
  }
  return describeSource(d, app)
}

/** A code fence that the log itself cannot close. */
function fenced(lines: string[]): string {
  const body = lines.join('\n')
  const longest = Math.max(0, ...(body.match(/`+/g) ?? []).map((m) => m.length))
  const fence = '`'.repeat(Math.max(3, longest + 1))
  return `${fence}\n${body}\n${fence}`
}

function tidy(lines: string[]): string[] {
  return lines
    .map((l) => (l.length > MAX_LINE ? l.slice(0, MAX_LINE) + ' ...' : l))
    .slice(-LOG_LINES)
}

/** The log tail for a failed deployment: its build log, which also holds the start and health check steps. */
async function deploymentLog(d: Deployment): Promise<{ title: string; lines: string[] } | null> {
  const title = d.error?.code === 'build_failed' ? 'Last lines of the build log' : 'Last lines of the deployment log'
  try {
    const lines = await fetchLogLines({ slug: d.app_slug, kind: 'build', deploymentId: d.id })
    if (lines.length) return { title, lines: tidy(lines) }
  } catch {
    // fall back to the short tail kept with the deployment
  }
  return d.log_tail?.length ? { title, lines: tidy(d.log_tail) } : null
}

async function runtimeLog(app: App): Promise<{ title: string; lines: string[] } | null> {
  try {
    const lines = await fetchLogLines({ slug: app.slug, kind: 'runtime' })
    if (lines.length) return { title: 'Last lines of the app log', lines: tidy(lines) }
  } catch {
    // the text still helps without the log
  }
  return null
}

function header(app: App): string[] {
  const out = [`App: ${app.name}`]
  if (app.type === 'web' && app.url) out.push(`URL: ${app.url}`)
  return out
}

const CLOSING = 'Please find out what is wrong, fix it, and deploy the app again with bitploy.'

function failedDeploymentText(app: App, d: Deployment, log: { title: string; lines: string[] } | null): string {
  const lines = [`My app "${app.name}" failed to deploy on bitploy.`, '', ...header(app)]
  lines.push(`What failed: ${deploymentFailure(d)} (version #${d.seq})`)
  lines.push(`Source: ${sourceLine(d, app)}`)
  if (d.error?.message) lines.push(`Error: ${d.error.message}`)
  if (d.error?.hint) lines.push(`Hint: ${d.error.hint}`)
  const next = d.next_steps?.length ? d.next_steps.join(' ') : d.error?.next_tool ? `use the ${d.error.next_tool} tool` : ''
  if (next) lines.push(`Next step suggested by bitploy: ${next}`)
  lines.push('')
  lines.push(log ? `${log.title}:` : 'The log could not be loaded. Read it with the get_logs tool.')
  if (log) lines.push(fenced(log.lines))
  lines.push('', CLOSING)
  return lines.join('\n')
}

function crashText(app: App, dep: Deployment | null, log: { title: string; lines: string[] } | null): string {
  const what = app.status === 'crashed' ? 'The app keeps crashing and restarting' : 'The app stopped working'
  const lines = [`My app "${app.name}" on bitploy is not running.`, '', ...header(app)]
  lines.push(`What failed: ${what}${app.restart_count > 0 ? ` (restarted ${app.restart_count} ${app.restart_count === 1 ? 'time' : 'times'} by itself)` : ''}`)
  if (dep) lines.push(`Source: ${sourceLine(dep, app)}`)
  if (app.status_message) lines.push(`Status: ${app.status_message}`)
  lines.push('Next step suggested by bitploy: read the app log, fix the cause, then deploy again')
  lines.push('')
  lines.push(log ? `${log.title}:` : 'The log could not be loaded. Read it with the get_logs tool.')
  if (log) lines.push(fenced(log.lines))
  lines.push('', CLOSING)
  return lines.join('\n')
}

/**
 * The text to paste into an AI assistant when something failed. Never rejects:
 * if the log cannot be loaded the text says so. It holds no environment values.
 *
 * With a deployment it describes that failed deployment. Without one it
 * describes the running app: a crash, or (when nothing ever ran) the newest failed deployment.
 */
export async function buildFailureText(app: App, deployment?: Deployment | null): Promise<string> {
  let d = deployment ?? null
  if (!d && !app.current_deployment) {
    try {
      d = (await api.deployments(app.slug)).find((x) => x.state === 'failed') ?? null
    } catch {
      d = null
    }
  }
  if (d) return failedDeploymentText(app, d, await deploymentLog(d))
  return crashText(app, app.current_deployment, await runtimeLog(app))
}
