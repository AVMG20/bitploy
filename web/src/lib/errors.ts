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
  dockerfile_missing: 'Add a Dockerfile at the top of your project, or set Build as to Static site if it is a plain website.',
  apps_quota_exceeded: 'Delete an app you no longer need, or ask your admin to raise your limit.',
  server_app_limit: 'Delete an app you no longer need, or ask your admin to raise the limit.',
  slug_taken: 'Pick another name for the address.',
  storage_quota_exceeded: 'Delete an app you no longer need, turn off /data on another app, or ask your admin for more storage.',
  memory_quota_exceeded: 'Stop or delete another app, or ask your admin for more memory.',
  cpu_quota_exceeded: 'Stop or delete another app, or ask your admin for more processor time.',
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
  if (e instanceof ApiError) return { code: e.code, message: e.message, hint: plainHint(e.code, e.hint) }
  if (e instanceof Error) return { code: 'error', message: e.message || 'Something went wrong.', hint: '' }
  return { code: 'error', message: 'Something went wrong.', hint: '' }
}
