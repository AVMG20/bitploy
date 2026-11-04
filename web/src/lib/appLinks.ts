import type { RouteLocationRaw } from 'vue-router'
import { projectIdOf } from './projects'
import type { App } from './types'

// Links to an app's page: /projects/<project>/apps/<slug>/<tab>. Inside a
// project page vue-router fills in the project from the current address, so
// a tab link only needs the slug; from anywhere else use these.

/** A tab of an app's page (route names as in router.ts: app, app-logs, app-variables...). */
export function appRoute(app: Pick<App, 'id' | 'slug' | 'project'>, name = 'app', query?: Record<string, string>): RouteLocationRaw {
  return { name, params: { projectId: projectIdOf(app), slug: app.slug }, query }
}

/** When only the slug is known: the old address, which finds the project and redirects. */
export function appPath(slug: string, tab = ''): string {
  return `/apps/${encodeURIComponent(slug)}${tab ? `/${tab}` : ''}`
}

export function projectRoute(id: string, query?: Record<string, string>): RouteLocationRaw {
  return { name: 'project', params: { projectId: id }, query }
}
