import { createRouter, createWebHistory } from 'vue-router'
import { api, ApiError } from './lib/api'
import { queryClient } from './lib/queryClient'
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
            { path: 'settings', name: 'app-settings', component: () => import('./pages/app/SettingsTab.vue'), props: true },
          ],
        },
        { path: 'tokens', name: 'tokens', component: () => import('./pages/TokensPage.vue'), meta: { title: 'Tokens' } },
        { path: 'connect', name: 'connect', component: () => import('./pages/ConnectPage.vue'), meta: { title: 'Connect AI' } },
        { path: 'settings', name: 'settings', component: () => import('./pages/SettingsPage.vue'), meta: { title: 'Settings' } },
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
  if (to.name === 'login' && session) {
    const next = typeof to.query.next === 'string' && to.query.next.startsWith('/') ? to.query.next : '/'
    return { path: next }
  }
  if (to.meta.public) return true
  if (!session) return { name: 'login', query: { next: to.fullPath } }
  if (to.meta.admin && session.user.role !== 'admin') return { name: 'overview' }
  return true
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} - bitploy` : 'bitploy'
})
