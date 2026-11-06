<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowUpRight } from 'lucide-vue-next'
import RelativeTime from '@/components/RelativeTime.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import StatusDot from '@/components/StatusDot.vue'
import { projectRoute } from '@/lib/appLinks'
import { formatMB, hostOf } from '@/lib/format'
import { aliasOf, appRole, isPublicApp, mainApp, projectHealth, projectHealthText, projectUsage, sortProjectApps } from '@/lib/projects'
import type { Project, ServiceTemplate } from '@/lib/types'

// A project on the home page: its name, whether everything runs, its address,
// and a small board with a chip per app (what it is, its short name, its status).
const props = defineProps<{ project: Project; templates: readonly ServiceTemplate[] }>()

const apps = computed(() => sortProjectApps(props.project.apps, props.templates))
const main = computed(() => mainApp(props.project))
const health = computed(() => projectHealth(props.project.apps))
const usage = computed(() => projectUsage(props.project.apps))
const MAX_CHIPS = 6
const lastAt = computed(() => {
  let at = ''
  for (const a of props.project.apps) {
    const d = a.current_deployment?.finished_at ?? a.current_deployment?.queued_at
    if (d && d > at) at = d
  }
  return at
})
const healthText = computed(() => projectHealthText(props.project.apps))
</script>

<template>
  <article class="group relative flex min-w-0 flex-col overflow-hidden rounded-xl border bg-card shadow-card transition-colors hover:border-foreground/25">
    <div class="flex min-w-0 items-start justify-between gap-3 px-4 pt-4">
      <div class="min-w-0">
        <h3 class="truncate font-medium">
          <RouterLink :to="projectRoute(project.id)" class="after:absolute after:inset-0 after:rounded-xl focus-visible:outline-none">{{ project.name }}</RouterLink>
        </h3>
        <a
          v-if="main && isPublicApp(main)"
          :href="main.url"
          target="_blank"
          rel="noopener noreferrer"
          class="relative z-10 mt-0.5 inline-flex max-w-full items-center gap-1 font-mono text-xs text-muted-foreground hover:text-foreground hover:underline"
        >
          <span class="truncate">{{ hostOf(main.url) }}</span><ArrowUpRight class="size-3 shrink-0" />
        </a>
        <p v-else class="mt-0.5 text-xs text-muted-foreground">{{ apps.length ? 'No web address' : 'No apps yet' }}</p>
      </div>
      <span class="inline-flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
        <StatusDot :status="health.status === 'empty' ? 'new' : health.status" aria-hidden="true" />
        <span>{{ healthText }}</span>
      </span>
    </div>

    <!-- The apps, as chips on a small board. -->
    <div class="canvas-dots mx-4 mt-3 flex min-h-20 flex-wrap content-center items-center gap-1.5 rounded-lg border p-2.5">
      <span
        v-for="a in apps.slice(0, MAX_CHIPS)"
        :key="a.id"
        class="inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-md border bg-card py-1 pr-2 pl-1 text-xs shadow-card"
        :title="`${a.name}: ${appRole(a, templates).label}`"
      >
        <ServiceIcon :role="appRole(a, templates).key" size="sm" class="size-5 rounded [&>svg]:size-3" />
        <span class="truncate font-mono">{{ aliasOf(a) }}</span>
        <StatusDot :status="a.status" />
      </span>
      <span v-if="apps.length > MAX_CHIPS" class="px-1 text-xs text-muted-foreground">and {{ apps.length - MAX_CHIPS }} more</span>
      <span v-if="!apps.length" class="w-full text-center text-xs text-muted-foreground">Open it to add an app or a database.</span>
    </div>

    <div class="mt-auto flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 px-4 pt-3 pb-3.5 text-xs text-muted-foreground">
      <span>{{ apps.length === 1 ? '1 app' : `${apps.length} apps` }}</span>
      <span v-if="usage.memoryMB" class="tabular-nums">{{ formatMB(usage.memoryMB) }} memory</span>
      <span v-if="lastAt" class="ml-auto">Deployed <RelativeTime :date="lastAt" /></span>
    </div>
  </article>
</template>
