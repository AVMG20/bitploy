// Deploy commands: a shell command bitploy runs before a new version takes
// over (database migrations) and one it runs in the live version afterwards.
// Only erasable TypeScript, so Node can run the file in a test
// (tests/deployCommands.test.ts).

import { isMulti } from './services.ts'
import type { App } from './types'

/** The longest command the server stores. */
export const MAX_DEPLOY_COMMAND = 2000

type AppLike = Pick<App, 'template' | 'catalog_id' | 'static' | 'build_mode' | 'services' | 'service_layout' | 'before_deploy_command' | 'after_deploy_command'>

/**
 * Whether the Settings tab offers deploy commands: web and worker apps built
 * from code with one service. Not for databases and one-click apps, static
 * sites or compose apps. An app that already has commands always shows them,
 * so they can be removed.
 */
export function offersDeployCommands(app: AppLike | null | undefined): boolean {
  if (!app) return false
  if (app.before_deploy_command || app.after_deploy_command) return true
  if (app.template || app.catalog_id || app.static || isMulti(app)) return false
  return !['static', 'static_build', 'compose'].includes(app.build_mode)
}

/** Why a command cannot be saved, or "". */
export function deployCommandError(cmd: string): string {
  if (cmd.trim().length > MAX_DEPLOY_COMMAND) return `Use at most ${MAX_DEPLOY_COMMAND} characters. Put longer steps in a script in your code and run that.`
  return ''
}
