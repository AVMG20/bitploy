import { ApiError } from './api'

export interface FriendlyError {
  code: string
  message: string
  hint: string
}

// The API's hints talk to an AI assistant ("call delete_app"). For the errors a
// person can run into on the New app form or in a first deploy, say it in
// dashboard words.
const HINTS: Record<string, string> = {
  github_access_missing: 'Check which repositories bitploy may read in Settings > GitHub, then try again.',
  dockerfile_missing: 'Add a Dockerfile at the top of your project, or choose Plain website as the type if it is a plain website.',
  apps_quota_exceeded: 'Delete an app you no longer need, or ask your admin to raise your limit.',
  server_app_limit: 'Delete an app you no longer need, or ask your admin to raise the limit.',
  slug_taken: 'Pick another name for the address.',
  storage_quota_exceeded: 'Delete an app you no longer need, turn off /data on another app, or ask your admin for more storage.',
  memory_quota_exceeded: 'Stop or delete another app, or ask your admin for more memory.',
  cpu_quota_exceeded: 'Stop or delete another app, or ask your admin for more CPU.',
  server_disk_full: 'Tell your admin that the server disk is full. Your running apps are not affected.',
}

// Words like delete_app or volume_mb are names of AI tools and API fields.
const TOOL_TALK = /\b[a-z]+(?:_[a-z]+)+\b/

/** The hint of an API error for a person: dashboard words, or nothing when it only makes sense to an AI. */
export function plainHint(code: string, hint: string | undefined): string {
  return HINTS[code] ?? (!hint || TOOL_TALK.test(hint) ? '' : hint)
}

/** Message and hint of an error, in plain words. */
export function friendlyError(e: unknown): FriendlyError {
  if (e instanceof ApiError && isComposeCode(e.code)) {
    const d = e.details as { service?: string; key?: string } | undefined
    const t = composeIssueText({ code: e.code, message: e.message, hint: e.hint, service: d?.service, key: d?.key })
    return { code: e.code, message: t.title, hint: [t.detail, t.hint].filter(Boolean).join(' ') }
  }
  if (e instanceof ApiError) return { code: e.code, message: e.message, hint: plainHint(e.code, e.hint) }
  if (e instanceof Error) return { code: 'error', message: e.message || 'Something went wrong.', hint: '' }
  return { code: 'error', message: 'Something went wrong.', hint: '' }
}

// ---------------------------------------------------------------- compose files
// What each compose problem means, in a sentence a beginner can follow. The
// server's message (it names the service and key) and hint follow it.
const COMPOSE_TITLES: Record<string, string> = {
  compose_invalid: 'bitploy cannot read this compose file.',
  compose_forbidden: 'The compose file asks for something bitploy does not allow.',
  compose_unsupported: 'The compose file uses something bitploy cannot run yet.',
  compose_public_ambiguous: 'Several services could be the website.',
  compose_too_many_services: 'This compose file has too many services.',
  compose_no_services: 'This compose file has no services.',
  compose_volume_shared: 'Two services use the same volume.',
  compose_env_file_missing: 'A file the compose file needs is missing.',
  compose_service_name_invalid: 'A service name cannot be used.',
  compose_file_missing: 'There is no compose file.',
  compose_source_missing: 'A folder the compose file builds from is missing.',
  compose_key_ignored: 'Part of the compose file is left out.',
  compose_variable_unset: 'A variable the compose file uses has no value.',
  compose_service_skipped: 'A service is left out.',
  compose_ports_not_published: 'Ports are not opened to the internet.',
}
const COMPOSE_HINTS: Record<string, string> = {
  compose_public_ambiguous: 'Add "x-bitploy: {public: true}" to the service visitors should open, or give only that one a ports: entry.',
  compose_too_many_services: 'A project can have up to 6 services. Leave out the ones you do not need, or split it into two projects.',
  compose_variable_unset: 'Add it under Variables (shared) after creating the project, or write the value in the file.',
  compose_ports_not_published: 'Only the website gets an address. The other services reach each other by name, like postgres:5432.',
}
// Hints that name AI tools are for the assistant, not for a person.
const AI_TOOLS = /\b(validate_compose|deploy_files|create_app|get_app|set_env|get_logs|restart_app)\b/

export interface ComposeIssueText {
  /** What it means, in plain words. */
  title: string
  /** The server's own words: which service and key. */
  detail: string
  hint: string
}

export function isComposeCode(code: string): boolean {
  return code.startsWith('compose_')
}

/** A compose problem for a person: what it means, what the server said and what to do. */
export function composeIssueText(i: { code: string; message: string; hint?: string; service?: string; key?: string }): ComposeIssueText {
  const hint = COMPOSE_HINTS[i.code] ?? (!i.hint || AI_TOOLS.test(i.hint) ? '' : i.hint)
  const title = COMPOSE_TITLES[i.code] ?? i.message
  let detail = title === i.message ? '' : i.message
  // Say where, when the message does not.
  if (detail && i.service && !detail.includes(i.service)) detail = `${i.service}${i.key ? ` (${i.key})` : ''}: ${detail}`
  return { title, detail, hint }
}
