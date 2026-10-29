import { createApp } from 'vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import App from './App.vue'
import { router } from './router'
import { onUnauthorized, setCsrfToken } from './lib/api'
import { initTheme } from './lib/theme'
import { queryClient } from './lib/queryClient'
import './style.css'

initTheme()

onUnauthorized(() => {
  setCsrfToken('')
  queryClient.clear()
  const current = router.currentRoute.value
  if (current.name !== 'login' && !current.meta.public) {
    void router.replace({ name: 'login', query: { next: current.fullPath } })
  }
})

const app = createApp(App)
app.use(router)
app.use(VueQueryPlugin, { queryClient: queryClient })
app.mount('#app')
