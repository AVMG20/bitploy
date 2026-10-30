<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { ChevronRight } from 'lucide-vue-next'
import { SidebarInset, SidebarProvider, SidebarTrigger } from '@/components/ui/sidebar'
import AppSidebar from './AppSidebar.vue'

const route = useRoute()

// Where you are, shown next to the sidebar toggle: "Apps › my-app".
const crumbs = computed(() => {
  const slug = route.params.slug
  if (typeof slug === 'string' && slug) return [{ label: 'Apps', to: '/' }, { label: slug }]
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
      </header>
      <main class="mx-auto w-full max-w-6xl flex-1 px-4 py-6 md:px-8 md:py-8">
        <RouterView />
      </main>
    </SidebarInset>
  </SidebarProvider>
</template>
