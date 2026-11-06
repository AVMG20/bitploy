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
  dockerfile_missing: 'Add a Dockerfile at the top of your code folder. For a site of HTML files, set the type to Plain website.',
  apps_quota_exceeded: 'Delete an app you no longer need, or ask your admin to raise your limit.',
  server_app_limit: 'Delete an app you no longer need, or ask your admin to raise the limit.',
  slug_taken: 'Pick another name for the address.',
  storage_quota_exceeded: 'Delete an app you no longer need, turn off saved files on another app, or ask your admin for more storage.',
  memory_quota_exceeded: 'Stop or delete another app, or ask your admin for more memory.',
  cpu_quota_exceeded: 'Stop or delete another app, or ask your admin for more CPU.',
  server_disk_full: 'Tell your admin that the server disk is full. Your running apps are not affected.',
  // projects, service templates and ${{name.VAR}} references
  project_not_found: 'The project was deleted or renamed. Open Projects to find it.',
  project_name_invalid: 'Use 1 to 80 characters for the project name.',
  project_required: 'Pick the project to put it in.',
  project_ambiguous: 'Two projects have that name. Rename one of them first.',
  projects_limit: 'You have 100 projects, the maximum. Delete one you no longer need.',
  project_not_empty: 'Delete its apps first, or tick the box that deletes them too.',
  confirm_mismatch: 'Type the name exactly as shown to confirm.',
  alias_invalid: 'Use lowercase letters, numbers and dashes for the short name, like db or api-2.',
  alias_taken: 'Another app of the project has that short name. Pick another, like db-2.',
  app_in_use: 'Other apps of the project read its variables. Remove those variables from them first, then delete it.',
  service_template_unknown: 'Pick one from the list under Add.',
  connect_to_other_project: 'An app can only connect to databases and services of its own project.',
  connect_to_var_set: 'That app already has this variable. Change it under its Variables, or untick it.',
  variable_reference_unknown: 'A ${{name.VAR}} value names an app or variable the project does not have. Check the spelling under Variables.',
  variable_reference_self: 'A variable cannot refer to its own app. Type the value instead.',
  variable_reference_loop: 'Two variables refer to each other. Change one of them to a plain value.',
  // public ports
  public_ports_off: 'This server hands out no public ports. Ask your admin to turn them on.',
  public_port_no_port: 'bitploy does not know the port it listens on. Set the port in Settings (or expose it in the compose file), deploy, then try again.',
  public_port_limit: 'Close the public port of another app first, or ask your admin to raise the limit.',
  public_ports_full: 'Every public port of the server is in use. Try again later, or ask your admin for a bigger range.',
  public_port_busy: 'Wait a few seconds and try again.',
  public_port_changed: 'The port was opened, changed or closed at the same moment. Reload the page to see it as it is now, then try again.',
  public_port_failed: 'The port is saved and bitploy keeps trying to start it. Check again in a minute.',
  my_ip_unknown: 'Pick These addresses and type your IPv4 address instead.',
  my_ip_not_ipv4: 'Pick These addresses and type your IPv4 address instead.',
  allow_from_invalid: 'Use IPv4 addresses like 203.0.113.7 or networks like 198.51.100.0/24, separated by commas.',
  service_required: 'Pick the service to open.',
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
  compose_unsupported: 'The compose file uses something bitploy cannot run.',
  compose_public_ambiguous: 'Several services could be the website.',
  compose_too_many_services: 'This compose file has too many services.',
  compose_no_services: 'This compose file has no services.',
  compose_volume_shared: 'Two services use the same volume.',
  compose_env_file_missing: 'A file the compose file needs is missing.',
  compose_service_name_invalid: 'A service name cannot be used.',
  compose_file_missing: 'There is no compose file.',
  compose_source_missing: 'A folder the compose file builds from is missing.',
  compose_key_ignored: 'bitploy ignores a setting in the compose file.',
  compose_variable_unset: 'A variable the compose file uses has no value.',
  compose_service_skipped: 'A service is left out.',
  compose_ports_not_published: 'Ports are not opened to the internet.',
}
const COMPOSE_HINTS: Record<string, string> = {
  compose_public_ambiguous: 'Add "x-bitploy: {public: true}" to the service visitors should open, or give only that one a ports: entry.',
  compose_too_many_services: 'An app can have up to 6 services. Leave out the ones you do not need, or split them over two apps.',
  compose_variable_unset: 'Add it under Variables, Shared, once the app is created, or write the value in the file.',
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
