<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowUpRight, Cable, HardDrive, Lock } from 'lucide-vue-next'
import ServiceIcon from '@/components/ServiceIcon.vue'
import Sparkline from '@/components/Sparkline.vue'
import StatusDot from '@/components/StatusDot.vue'
import { useAppMetrics } from '@/composables/queries'
import { projectRoute } from '@/lib/appLinks'
import { appStatusInfo, cpuFromPercent, formatCPU, formatMB, hostOf } from '@/lib/format'
import { aliasOf, appSourceText, internalHostOf, isPublicApp, projectIdOf, roleTitle, templateUpdate } from '@/lib/projects'
import type { AppRole } from '@/lib/projects'
import { isMulti, serviceRole, serviceStatus, sortServices } from '@/lib/services'
import type { App, ServiceTemplate } from '@/lib/types'
import { cn } from '@/lib/utils'

// One app on the project canvas: what it is for, whether it runs, where it is
// reached, how busy it is, and which apps it talks to. A compose app shows its
// services inside. The whole card opens the app's panel over the canvas.
const props = defineProps<{
  app: App
  role: AppRole
  templates: readonly ServiceTemplate[]
  talksTo: readonly App[]
  usedBy: readonly App[]
  /** Another card is hovered and this one is not linked to it. */
  dim?: boolean
  /** Linked to the hovered card. */
  lit?: boolean
  /** Its panel is open. */
  selected?: boolean
}>()
const emit = defineEmits<{ hover: [id: string | null] }>()

const running = computed(() => props.app.status === 'running')
const status = computed(() => appStatusInfo(props.app.status))
const publicApp = computed(() => isPublicApp(props.app))
const host = computed(() => internalHostOf(props.app))
const metrics = useAppMetrics(() => props.app.slug, '1h', running, '', true)
const points = computed(() => metrics.data.value?.points ?? [])
const latest = computed(() => (running.value ? (metrics.data.value?.latest ?? props.app.live ?? points.value.at(-1) ?? null) : null))
// Data keepers show memory (what they hold); the rest show CPU (how busy).
const showMemory = computed(() => props.role.lane === 'data')
const spark = computed(() => (showMemory.value ? points.value.map((p) => p.memory_mb) : points.value.map((p) => cpuFromPercent(p.cpu))))
const reading = computed(() => {
  if (!latest.value) return '-'
  return showMemory.value ? `${formatMB(latest.value.memory_mb)} of ${formatMB(props.app.memory_mb)}` : formatCPU(cpuFromPercent(latest.value.cpu))
})
const services = computed(() => (isMulti(props.app) ? sortServices(props.app.services) : []))
const storedMB = computed(() => props.app.volumes.reduce((n, v) => n + (v.used_mb ?? 0), 0))
const source = computed(() => appSourceText(props.app, props.templates))
const panelOf = (a: App) => projectRoute(projectIdOf(a), { app: a.slug })
</script>

<template>
  <article
    :data-app="app.id"
    :class="cn(
      'group relative flex w-full min-w-0 flex-col gap-3 rounded-xl border bg-card p-4 shadow-card transition-[border-color,opacity,box-shadow] duration-150 hover:border-foreground/25 focus-within:border-foreground/25',
      lit && 'border-primary/50 ring-1 ring-primary/20',
      selected && 'border-primary ring-2 ring-primary/30 hover:border-primary',
      dim && 'opacity-45',
    )"
    @mouseenter="emit('hover', app.id)"
    @mouseleave="emit('hover', null)"
    @focusin="emit('hover', app.id)"
    @focusout="emit('hover', null)"
  >
    <div class="flex min-w-0 items-start gap-3">
      <ServiceIcon :role="role.key" />
      <div class="min-w-0 flex-1">
        <h3 class="flex min-w-0 items-baseline gap-2">
          <RouterLink :to="panelOf(app)" :aria-current="selected ? 'true' : undefined" class="max-w-full shrink-0 truncate font-medium after:absolute after:inset-0 after:rounded-xl focus-visible:outline-none">
            {{ app.name }}
          </RouterLink>
          <span v-if="aliasOf(app) !== app.name" class="min-w-0 truncate font-mono text-xs text-muted-foreground">{{ aliasOf(app) }}</span>
        </h3>
        <p class="truncate text-xs text-muted-foreground">{{ roleTitle(role) }}</p>
      </div>
      <span class="inline-flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
        <StatusDot :status="app.status" /> <span aria-hidden="true">{{ status.label }}</span>
      </span>
    </div>

    <!-- Where it is reached. -->
    <div class="relative z-10 min-w-0 text-xs">
      <a v-if="publicApp" :href="app.url" target="_blank" rel="noopener noreferrer" class="inline-flex max-w-full items-center gap-1 font-mono text-foreground hover:underline">
        <span class="truncate">{{ hostOf(app.url) }}</span><ArrowUpRight class="size-3 shrink-0 text-muted-foreground" />
      </a>
      <span v-else-if="host" class="inline-flex max-w-full items-center gap-1.5 text-muted-foreground" :title="`The other apps of this project reach it at ${host}. The internet cannot.`">
        <Lock class="size-3 shrink-0" /> Private, at <span class="truncate font-mono text-foreground/80">{{ host }}</span>
      </span>
      <span v-else class="inline-flex items-center gap-1.5 text-muted-foreground"><Lock class="size-3 shrink-0" /> No web address</span>
    </div>

    <!-- A compose app: its services, each with its own status. -->
    <ul v-if="services.length" class="relative z-10 grid gap-1 rounded-lg border bg-muted/30 p-1.5" :aria-label="`Services of ${app.name}`">
      <li v-for="s in services" :key="s.name" class="flex min-w-0 items-center gap-2 rounded-md px-1.5 py-1 text-xs">
        <ServiceIcon :role="serviceRole(s).key" size="sm" class="size-5 rounded-md [&>svg]:size-3" />
        <span class="truncate font-mono">{{ s.name }}</span>
        <span class="truncate text-muted-foreground">{{ serviceRole(s).label }}</span>
        <span class="ml-auto flex shrink-0 items-center gap-2">
          <span v-if="s.depends_on?.length" class="hidden text-muted-foreground sm:inline" :title="`${s.name} reaches ${s.depends_on.join(', ')} by name`">uses {{ s.depends_on.join(', ') }}</span>
          <StatusDot :status="serviceStatus(app, s)" />
        </span>
      </li>
    </ul>

    <div class="flex items-end justify-between gap-3">
      <div class="min-w-0 flex-1">
        <p class="flex items-baseline justify-between gap-2 text-[11px]">
          <span class="text-muted-foreground">{{ showMemory ? 'Memory' : 'CPU' }}, last hour</span>
          <span class="font-mono tabular-nums">{{ reading }}</span>
        </p>
        <Sparkline
          class="mt-1"
          :values="spark"
          :max="showMemory ? app.memory_mb : app.cpu_millis / 1000"
          :color="showMemory ? 'var(--chart-2)' : 'var(--chart-1)'"
          :format="(v) => (showMemory ? formatMB(v) : formatCPU(v))"
          :label="`${showMemory ? 'Memory' : 'CPU'} of ${app.name} in the last hour`"
        />
      </div>
    </div>

    <div class="mt-auto grid gap-1.5 border-t pt-3 text-xs text-muted-foreground">
      <p v-for="row in [{ label: 'Talks to', apps: talksTo }, { label: 'Used by', apps: usedBy }].filter((r) => r.apps.length)" :key="row.label" class="flex min-w-0 flex-wrap items-center gap-1.5">
        <Cable class="size-3 shrink-0" /> <span>{{ row.label }}</span>
        <RouterLink
          v-for="t in row.apps"
          :key="t.id"
          :to="panelOf(t)"
          class="relative z-10 rounded-md border bg-muted/50 px-1.5 py-0.5 font-mono text-[11px] text-foreground/85 hover:border-foreground/30"
        >{{ aliasOf(t) }}</RouterLink>
      </p>
      <p class="flex min-w-0 items-center gap-1.5">
        <span class="truncate">{{ source }}</span>
        <span v-if="templateUpdate(app)" class="relative z-10 shrink-0 rounded border px-1 text-[10.5px]" :title="templateUpdate(app)!.title">{{ templateUpdate(app)!.text }}</span>
        <span v-if="app.volumes.length && role.lane === 'data'" class="ml-auto inline-flex shrink-0 items-center gap-1" title="Saved files, kept between deploys and backed up">
          <HardDrive class="size-3" /> {{ formatMB(storedMB) }}
        </span>
      </p>
    </div>
  </article>
</template>
