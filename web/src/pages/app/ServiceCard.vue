<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { ArrowRight, ExternalLink, HardDrive, Lock } from 'lucide-vue-next'
import AppStatusBadge from '@/components/AppStatusBadge.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import Sparkline from '@/components/Sparkline.vue'
import { useAppMetrics } from '@/composables/queries'
import { cpuFromPercent, formatCPU, formatMB, hostOf } from '@/lib/format'
import { listWords, serviceRole, serviceSource, serviceStatus } from '@/lib/services'
import type { App, AppService } from '@/lib/types'

// One service of a project in the grid: what it does in plain words, whether it
// runs, how much it uses, and which other services it talks to. The whole card
// opens the service's panel (?service=<name> on the project page).
const props = defineProps<{ app: App; service: AppService }>()
const route = useRoute()

const role = computed(() => serviceRole(props.service))
const status = computed(() => serviceStatus(props.app, props.service))
const running = computed(() => status.value === 'running')
const metrics = useAppMetrics(() => props.app.slug, '1h', running, () => props.service.name, true)
const points = computed(() => metrics.data.value?.points ?? [])
// The newest reading; the last point of the hour when the live one is missing.
const latest = computed(() => (running.value ? (metrics.data.value?.latest ?? props.service.live ?? points.value[points.value.length - 1] ?? null) : null))
const cpuLimit = computed(() => props.service.cpu_millis / 1000)
const volumes = computed(() => props.app.volumes.filter((v) => props.service.volumes?.includes(v.name)))
const detail = computed(() => {
  const src = serviceSource(props.service)
  return role.value.product ? `${role.value.product}` : src
})
// "188 / 512 MB": the unit once when both are in MB, so it fits a narrow card.
function memNow(mb: number): string {
  const now = formatMB(mb)
  return now.endsWith(' MB') && formatMB(props.service.memory_mb).endsWith(' MB') ? now.slice(0, -3) : now
}
const to = computed(() => ({ query: { ...route.query, service: props.service.name } }))
</script>

<template>
  <article class="group relative flex min-w-0 flex-col gap-4 rounded-xl border bg-card p-4 shadow-card transition-colors hover:border-foreground/20">
    <div class="flex min-w-0 items-start gap-3">
      <ServiceIcon :role="role.key" />
      <div class="min-w-0 flex-1">
        <h3 class="flex min-w-0 items-baseline gap-2">
          <!-- The link covers the card, so the whole card opens the panel. -->
          <RouterLink :to="to" class="truncate font-medium after:absolute after:inset-0 after:rounded-xl focus-visible:outline-none" :aria-label="`${role.label} ${service.name}: open its logs, usage and settings`">
            {{ role.label }}
          </RouterLink>
          <span class="truncate font-mono text-xs text-muted-foreground">{{ service.name }}</span>
        </h3>
        <p class="truncate text-xs text-muted-foreground" :title="service.image || undefined">
          {{ detail }}<template v-if="service.kind === 'image' && role.product"> · <span class="font-mono">{{ service.image }}</span></template>
        </p>
      </div>
      <AppStatusBadge :status="status" class="shrink-0" />
    </div>

    <!-- Where it can be reached: the website at the project address, the rest only inside the project. -->
    <div class="relative z-10 -mt-1 min-w-0 text-xs">
      <a
        v-if="service.public && app.type === 'web'"
        :href="app.url"
        target="_blank"
        rel="noopener noreferrer"
        class="inline-flex max-w-full items-center gap-1 font-mono text-foreground hover:underline"
      >
        <span class="truncate">{{ hostOf(app.url) }}</span><ExternalLink class="size-3 shrink-0 text-muted-foreground" />
      </a>
      <span v-else-if="service.port" class="inline-flex max-w-full items-center gap-1.5 text-muted-foreground" :title="`The other services of this project reach it at ${service.name}:${service.port}. Visitors cannot.`">
        <Lock class="size-3 shrink-0" /> Private, at <span class="truncate font-mono text-foreground/80">{{ service.name }}:{{ service.port }}</span>
      </span>
      <span v-else class="inline-flex items-center gap-1.5 text-muted-foreground"><Lock class="size-3 shrink-0" /> No web address</span>
    </div>

    <div class="grid grid-cols-2 gap-4">
      <div class="min-w-0">
        <p class="flex items-baseline justify-between gap-2 text-xs">
          <span class="text-muted-foreground">CPU</span>
          <span class="font-mono whitespace-nowrap tabular-nums">{{ latest ? formatCPU(cpuFromPercent(latest.cpu)) : '-' }}</span>
        </p>
        <Sparkline class="mt-1.5" :values="points.map((p) => cpuFromPercent(p.cpu))" :max="cpuLimit" color="var(--chart-1)" :format="(v) => formatCPU(v)" :label="`CPU of ${service.name} in the last hour`" />
      </div>
      <div class="min-w-0">
        <p class="flex items-baseline justify-between gap-2 text-xs">
          <span class="text-muted-foreground">Memory</span>
          <span class="font-mono whitespace-nowrap tabular-nums">{{ latest ? memNow(latest.memory_mb) : '-' }}<span class="text-muted-foreground"> / {{ formatMB(service.memory_mb) }}</span></span>
        </p>
        <Sparkline class="mt-1.5" :values="points.map((p) => p.memory_mb)" :max="service.memory_mb" color="var(--chart-2)" :format="(v) => formatMB(v)" :label="`Memory of ${service.name} in the last hour`" />
      </div>
    </div>

    <div class="mt-auto flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 border-t pt-3 text-xs text-muted-foreground">
      <span v-if="service.depends_on?.length" class="inline-flex min-w-0 items-center gap-1.5" :title="`${service.name} starts after ${listWords(service.depends_on)} and reaches them by name.`">
        <ArrowRight class="size-3 shrink-0" /> Talks to <span class="truncate font-mono text-foreground/80">{{ service.depends_on.join(', ') }}</span>
      </span>
      <span v-for="v in volumes" :key="v.name" class="inline-flex items-center gap-1.5" :title="`Stored data in ${v.mount_path}, kept between deploys.`">
        <HardDrive class="size-3 shrink-0" /> <span class="font-mono text-foreground/80">{{ v.name }}</span>
        {{ v.used_mb !== null ? `${formatMB(v.used_mb)} of ${formatMB(v.size_mb)}` : formatMB(v.size_mb) }}
      </span>
      <span v-if="!service.depends_on?.length && !volumes.length">Needs no other service and keeps no data</span>
    </div>
  </article>
</template>
