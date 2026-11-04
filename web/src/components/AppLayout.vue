<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { ChevronRight, Search } from 'lucide-vue-next'
import { Kbd } from '@/components/ui/kbd'
import { SidebarInset, SidebarProvider, SidebarTrigger } from '@/components/ui/sidebar'
import { useApp, useProject } from '@/composables/queries'
import { projectRoute } from '@/lib/appLinks'
import { useEvents } from '@/composables/useEvents'
import { modKey, paletteOpen, usePaletteShortcut } from '@/composables/useShortcuts'
import AppSidebar from './AppSidebar.vue'
import CommandPalette from './CommandPalette.vue'
import AddToProjectDialog from './project/AddToProjectDialog.vue'

const route = useRoute()
// Live updates for everything below: opens when you are signed in, closes when you leave (logout).
useEvents()
usePaletteShortcut()

// Where you are, shown next to the sidebar toggle: "Projects › Shop › web".
// The pages ask for the same app and projects, so this costs no extra request.
const slug = computed(() => (typeof route.params.slug === 'string' ? route.params.slug : ''))
const projectId = computed(() => (typeof route.params.projectId === 'string' ? route.params.projectId : ''))
const app = useApp(slug, () => !!slug.value)
const { project } = useProject(projectId)
const crumbs = computed(() => {
  const appName = app.data.value?.slug === slug.value ? app.data.value.name : slug.value
  if (projectId.value) {
    const name = project.value?.name ?? (app.data.value?.slug === slug.value ? app.data.value.project?.name : '') ?? ''
    const list: { label: string; to?: ReturnType<typeof projectRoute> | string }[] = [{ label: 'Projects', to: '/' }]
    if (slug.value) list.push({ label: name || 'Project', to: projectRoute(projectId.value) }, { label: appName })
    else list.push({ label: name || 'Project' })
    return list
  }
  if (slug.value) return [{ label: 'Projects', to: '/' }, { label: appName }]
  if (route.path.startsWith('/settings/')) return [{ label: 'Settings', to: '/settings' }, { label: String(route.meta.title ?? '') }]
  return [{ label: String(route.meta.title ?? '') }]
})

// On desktop the inset panel scrolls, not the window; reset it on navigation
// the same way the router's scrollBehavior resets the window.
const panel = ref<InstanceType<typeof SidebarInset>>()
watch(
  () => route.path,
  () => {
    const el = panel.value?.$el as HTMLElement | undefined
    el?.scrollTo({ top: 0 })
  },
)
</script>

<template>
  <SidebarProvider>
    <AppSidebar />
    <SidebarInset ref="panel" class="min-w-0 md:h-[calc(100svh-1rem)] md:overflow-y-auto">
      <header class="sticky top-0 z-10 flex h-12 shrink-0 items-center gap-2 border-b bg-background/85 px-3 backdrop-blur-md md:px-4">
        <SidebarTrigger class="text-muted-foreground" />
        <span class="mr-1 h-4 w-px bg-border" aria-hidden="true" />
        <nav aria-label="Breadcrumb" class="flex min-w-0 items-center gap-1 text-sm">
          <template v-for="(c, i) in crumbs" :key="i">
            <ChevronRight v-if="i > 0" class="size-3.5 shrink-0 text-muted-foreground/60" />
            <RouterLink v-if="c.to" :to="c.to" class="truncate text-muted-foreground transition-colors hover:text-foreground">{{ c.label }}</RouterLink>
            <span v-else class="truncate font-medium" aria-current="page">{{ c.label }}</span>
          </template>
        </nav>
        <button
          type="button"
          class="ml-auto inline-flex h-8 shrink-0 items-center gap-2 rounded-md border bg-card px-2 text-sm text-muted-foreground shadow-card transition-colors hover:bg-accent hover:text-foreground sm:w-56 sm:px-2.5"
          aria-label="Go to a project or a page"
          aria-keyshortcuts="Meta+K Control+K"
          @click="paletteOpen = true"
        >
          <Search class="size-4 shrink-0" />
          <span class="hidden flex-1 text-left sm:inline">Go to...</span>
          <Kbd class="hidden sm:inline-flex">{{ modKey }} K</Kbd>
        </button>
      </header>
      <main :class="['mx-auto w-full flex-1 px-4 py-6 md:px-8 md:py-8', route.meta.wide ? 'max-w-[110rem]' : 'max-w-6xl']">
        <RouterView />
      </main>
    </SidebarInset>
    <CommandPalette />
    <AddToProjectDialog />
  </SidebarProvider>
</template>
