import { createRouter, createWebHistory } from 'vue-router'
import type { RouteLocationNormalized } from 'vue-router'
import { api, ApiError } from './lib/api'
import { keys } from './lib/live'
import { projectIdOf } from './lib/projects'
import { queryClient } from './lib/queryClient'
import { rememberToken } from './lib/mcp'
import { clearSqlHistory } from './lib/dbvalue'
import { safeNext } from './lib/utils'
import { SESSION_KEY } from './composables/useSession'
import type { SessionInfo } from './lib/types'

declare module 'vue-router' {
  interface RouteMeta {
    public?: boolean
    admin?: boolean
    title?: string
    /** The tab of the app page that stays highlighted on a page below it. */
    tab?: string
    /** A page that gets more room than the usual column. */
    wide?: boolean
  }
}

/** The app's tabs take only the slug; the project id stays with the page around them. */
const slugProp = (r: RouteLocationNormalized) => ({ slug: String(r.params.slug) })

/** /apps/<slug>/<tab>: the address before projects. Find the app's project and go there. */
async function toProjectApp(to: RouteLocationNormalized) {
  const slug = String(to.params.slug)
  const rest = Array.isArray(to.params.rest) ? to.params.rest.join('/') : String(to.params.rest ?? '')
  let projectId = '-'
  try {
    const app = await queryClient.fetchQuery({ queryKey: keys.app(slug), queryFn: () => api.app(slug), staleTime: 10_000 })
    projectId = projectIdOf(app)
  } catch {
    // Unknown app: the app page says it cannot find it.
  }
  return { path: `/projects/${encodeURIComponent(projectId)}/apps/${encodeURIComponent(slug)}${rest ? `/${rest}` : ''}`, query: to.query, hash: to.hash, replace: true }
}

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('./pages/LoginPage.vue'),
      meta: { public: true, title: 'Log in' },
    },
    {
      path: '/invite/:code',
      name: 'invite',
      component: () => import('./pages/AcceptInvitePage.vue'),
      meta: { public: true, title: 'Join bitploy' },
    },
    {
      // An invite to someone's project: join it when logged in, else log in or sign up first.
      path: '/join/:code',
      name: 'join',
      component: () => import('./pages/JoinProjectPage.vue'),
      meta: { public: true, title: 'Join a project' },
    },
    {
      path: '/',
      component: () => import('./components/AppLayout.vue'),
      children: [
        { path: '', name: 'overview', component: () => import('./pages/ProjectsPage.vue'), meta: { title: 'Projects' } },
        // ?project=<id> adds the new app to that project; ?source= opens one source.
        { path: 'new', name: 'new-app', component: () => import('./pages/NewAppPage.vue'), meta: { title: 'New project' } },
        { path: 'new/:catalogId', name: 'new-catalog', component: () => import('./pages/NewCatalogPage.vue'), props: true, meta: { title: 'Ready-made app' } },
        { path: 'ready-made', name: 'ready-made', component: () => import('./pages/ReadyMadePage.vue'), meta: { title: 'Ready-made' } },
        {
          path: 'projects/:projectId',
          component: () => import('./pages/project/ProjectPage.vue'),
          props: true,
          meta: { wide: true },
          children: [
            // ?app=<slug> opens that app's panel over the canvas.
            { path: '', name: 'project', component: () => import('./pages/project/ProjectCanvas.vue'), props: true },
            { path: 'variables', name: 'project-variables', component: () => import('./pages/project/ProjectVariablesTab.vue'), props: true, meta: { title: 'Variables' } },
            { path: 'members', name: 'project-members', component: () => import('./pages/project/ProjectMembersTab.vue'), props: true, meta: { title: 'Members' } },
            { path: 'settings', name: 'project-settings', component: () => import('./pages/project/ProjectSettingsTab.vue'), props: true, meta: { title: 'Settings' } },
          ],
        },
        {
          path: 'projects/:projectId/apps/:slug',
          component: () => import('./pages/app/AppPage.vue'),
          props: true,
          children: [
            { path: '', name: 'app', component: () => import('./pages/app/AppOverviewTab.vue'), props: slugProp },
            { path: 'traffic', name: 'app-traffic', component: () => import('./pages/app/TrafficTab.vue'), props: slugProp },
            { path: 'deployments', name: 'app-deployments', component: () => import('./pages/app/DeploymentsTab.vue'), props: slugProp },
            { path: 'logs', name: 'app-logs', component: () => import('./pages/app/LogsTab.vue'), props: slugProp },
            { path: 'variables', name: 'app-variables', component: () => import('./pages/app/VariablesTab.vue'), props: slugProp },
            { path: 'domains', name: 'app-domains', component: () => import('./pages/app/DomainsTab.vue'), props: slugProp },
            { path: 'storage', name: 'app-storage', component: () => import('./pages/app/StorageTab.vue'), props: slugProp },
            {
              path: 'database',
              name: 'app-database-default',
              component: () => import('./pages/app/database/DatabasePage.vue'),
              props: slugProp,
              meta: { title: 'Database', wide: true },
            },
            {
              path: 'storage/db',
              name: 'app-database',
              component: () => import('./pages/app/database/DatabasePage.vue'),
              props: slugProp,
              meta: { title: 'Database', tab: 'app-storage', wide: true },
            },
            { path: 'settings', name: 'app-settings', component: () => import('./pages/app/SettingsTab.vue'), props: slugProp },
          ],
        },
        // Links and bookmarks from before projects: /apps/<slug>/<tab>.
        { path: 'apps/:slug/:rest(.*)*', name: 'legacy-app', component: () => import('./pages/NotFoundPage.vue'), beforeEnter: toProjectApp },
        { path: 'connect', name: 'connect', component: () => import('./pages/ConnectPage.vue'), meta: { title: 'Connect AI' } },
        {
          path: 'settings',
          component: () => import('./pages/settings/SettingsLayout.vue'),
          children: [
            { path: '', name: 'settings', component: () => import('./pages/settings/AccountTab.vue'), meta: { title: 'Settings' } },
            { path: 'tokens', name: 'settings-tokens', component: () => import('./pages/settings/TokensTab.vue'), meta: { title: 'API tokens' } },
            { path: 'github', name: 'settings-github', component: () => import('./pages/settings/GithubTab.vue'), meta: { title: 'GitHub' } },
            { path: 'sessions', name: 'settings-sessions', component: () => import('./pages/settings/SessionsTab.vue'), meta: { title: 'Sessions' } },
          ],
        },
        // Tokens used to have their own page; old links and bookmarks still work.
        { path: 'tokens', redirect: { name: 'settings-tokens' } },
        { path: 'admin', name: 'admin', component: () => import('./pages/AdminPage.vue'), meta: { title: 'Admin', admin: true } },
        {
          path: 'admin/database',
          name: 'admin-database',
          // Not wide: it sits under the Admin tab bar, which keeps the width of the other Admin tabs.
          component: () => import('./pages/admin/AdminDatabasePage.vue'),
          meta: { title: 'Database', admin: true },
        },
      ],
    },
    { path: '/:rest(.*)*', name: 'not-found', component: () => import('./pages/NotFoundPage.vue'), meta: { public: true, title: 'Not found' } },
  ],
  scrollBehavior: () => ({ top: 0 }),
})

async function loadSession(): Promise<SessionInfo | null> {
  try {
    return await queryClient.fetchQuery({ queryKey: SESSION_KEY, queryFn: api.session, staleTime: 5 * 60_000 })
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) return null
    // A hiccup (network, 5xx) must not log out someone whose session we already know.
    const cached = queryClient.getQueryData<SessionInfo>(SESSION_KEY)
    if (cached) return cached
    throw e
  }
}

router.beforeEach(async (to) => {
  let session: SessionInfo | null
  try {
    session = await loadSession()
  } catch {
    // Backend unreachable: public pages still render, the rest goes to login.
    return to.meta.public ? true : { name: 'login', query: { next: to.fullPath } }
  }
  // A token kept for the Connect page must not outlive the login that made it.
  if (!session) {
    rememberToken('')
    clearSqlHistory() // the SQL console's remembered queries can hold data
  }
  if (to.name === 'login' && session) {
    return { path: safeNext(to.query.next) }
  }
  if (to.meta.public) return true
  if (!session) return { name: 'login', query: { next: to.fullPath } }
  if (to.meta.admin && session.user.role !== 'admin') return { name: 'overview' }
  return true
})

router.afterEach((to, from) => {
  // Project and app pages set their own title once their name is known. A change of
  // the query only (a subtab of the database viewer, a filter) keeps the page's title.
  if (from.matched.length && to.path === from.path) return
  const slug = typeof to.params.slug === 'string' ? to.params.slug : ''
  const title = to.meta.title || slug
  document.title = title ? `${title} - bitploy` : 'bitploy'
})
