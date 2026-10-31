import { createRouter, createWebHistory } from 'vue-router'
import { api, ApiError } from './lib/api'
import { queryClient } from './lib/queryClient'
import { rememberToken } from './lib/mcp'
import { safeNext } from './lib/utils'
import { SESSION_KEY } from './composables/useSession'
import type { SessionInfo } from './lib/types'

declare module 'vue-router' {
  interface RouteMeta {
    public?: boolean
    admin?: boolean
    title?: string
  }
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
      path: '/',
      component: () => import('./components/AppLayout.vue'),
      children: [
        { path: '', name: 'overview', component: () => import('./pages/OverviewPage.vue'), meta: { title: 'Apps' } },
        {
          path: 'apps/:slug',
          component: () => import('./pages/app/AppPage.vue'),
          props: true,
          children: [
            { path: '', name: 'app', component: () => import('./pages/app/AppOverviewTab.vue'), props: true },
            { path: 'deployments', name: 'app-deployments', component: () => import('./pages/app/DeploymentsTab.vue'), props: true },
            { path: 'logs', name: 'app-logs', component: () => import('./pages/app/LogsTab.vue'), props: true },
            { path: 'domains', name: 'app-domains', component: () => import('./pages/app/DomainsTab.vue'), props: true },
            { path: 'storage', name: 'app-storage', component: () => import('./pages/app/StorageTab.vue'), props: true },
            { path: 'settings', name: 'app-settings', component: () => import('./pages/app/SettingsTab.vue'), props: true },
          ],
        },
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
  if (!session) rememberToken('')
  if (to.name === 'login' && session) {
    return { path: safeNext(to.query.next) }
  }
  if (to.meta.public) return true
  if (!session) return { name: 'login', query: { next: to.fullPath } }
  if (to.meta.admin && session.user.role !== 'admin') return { name: 'overview' }
  return true
})

router.afterEach((to) => {
  const slug = typeof to.params.slug === 'string' ? to.params.slug : ''
  const title = to.meta.title || slug
  document.title = title ? `${title} - bitploy` : 'bitploy'
})
