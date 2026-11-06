<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowRight } from 'lucide-vue-next'
import RelativeTime from '@/components/RelativeTime.vue'
import { Progress } from '@/components/ui/progress'
import { useAppMetrics } from '@/composables/queries'
import { CPU_HINT, cpuFromPercent, formatCPU, formatMB, pct, versionLabel } from '@/lib/format'
import { serviceStatus, sortServices } from '@/lib/services'
import type { App } from '@/lib/types'
import ServiceCard from './ServiceCard.vue'

// The overview of a project with several services: a strip with the whole
// project's numbers, then one card per service, the website first and the
// databases last.
const props = defineProps<{ app: App }>()

const services = computed(() => sortServices(props.app.services))
const runningCount = computed(() => props.app.services.filter((s) => serviceStatus(props.app, s) === 'running').length)
const memoryMB = computed(() => props.app.services.reduce((n, s) => n + s.memory_mb, 0))
const cpuLimit = computed(() => props.app.services.reduce((n, s) => n + s.cpu_millis, 0) / 1000)
const storedMB = computed(() => props.app.volumes.reduce((n, v) => n + (v.used_mb ?? 0), 0))
const storedLimit = computed(() => props.app.volumes.reduce((n, v) => n + v.size_mb, 0))
const measured = computed(() => props.app.volumes.some((v) => v.used_mb !== null))

const running = computed(() => props.app.status === 'running')
// Without a service the server sums every service.
const metrics = useAppMetrics(() => props.app.slug, '1h', running)
const live = computed(() => (running.value ? (metrics.data.value?.latest ?? props.app.live ?? metrics.data.value?.points?.at(-1) ?? null) : null))
const dep = computed(() => props.app.current_deployment)
</script>

<template>
  <div class="grid gap-4">
    <div class="grid grid-cols-2 overflow-hidden rounded-xl border bg-card shadow-card lg:grid-cols-4">
      <div class="border-b p-4 lg:border-b-0">
        <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Services</p>
        <p class="mt-1 text-xl font-semibold tracking-tight tabular-nums">
          {{ runningCount }} <span class="text-sm font-normal tracking-normal text-muted-foreground">of {{ app.services.length }} running</span>
        </p>
      </div>
      <div class="border-b border-l p-4 lg:border-b-0">
        <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Memory now</p>
        <p class="mt-1 text-xl font-semibold tracking-tight tabular-nums">
          {{ live ? formatMB(live.memory_mb) : '-' }}
          <span class="text-sm whitespace-nowrap font-normal tracking-normal text-muted-foreground">of {{ formatMB(memoryMB) }}</span>
        </p>
        <Progress class="mt-2 [&>[data-slot=progress-indicator]]:bg-success" :model-value="live ? pct(live.memory_mb, memoryMB) : 0" />
      </div>
      <div class="p-4 lg:border-l">
        <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">CPU now</p>
        <p class="mt-1 text-xl font-semibold tracking-tight tabular-nums">
          {{ live ? formatCPU(cpuFromPercent(live.cpu)).replace(' CPU', '') : '-' }}
          <span class="text-sm font-normal tracking-normal text-muted-foreground" :title="CPU_HINT">of {{ formatCPU(cpuLimit) }}</span>
        </p>
        <Progress class="mt-2" :model-value="live ? pct(cpuFromPercent(live.cpu), cpuLimit) : 0" />
      </div>
      <RouterLink :to="{ name: 'app-storage', params: { slug: app.slug } }" class="border-l p-4 transition-colors hover:bg-muted/40">
        <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Saved files</p>
        <p class="mt-1 text-xl font-semibold tracking-tight tabular-nums">
          <template v-if="!app.volumes.length"><span class="text-sm font-normal tracking-normal text-muted-foreground">None</span></template>
          <template v-else>
            {{ measured ? formatMB(storedMB) : '-' }}
            <span class="text-sm font-normal tracking-normal text-muted-foreground">of {{ formatMB(storedLimit) }}</span>
          </template>
        </p>
        <p v-if="app.volumes.length" class="mt-1 text-xs text-muted-foreground">In {{ app.volumes.length === 1 ? '1 volume' : `${app.volumes.length} volumes` }}, kept between deploys</p>
      </RouterLink>
    </div>

    <section class="grid gap-3" aria-labelledby="services-title">
      <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <h2 id="services-title" class="text-sm font-medium">What runs in this app</h2>
        <p class="text-xs text-muted-foreground">Open a service to see its logs, usage and settings.</p>
      </div>
      <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        <ServiceCard v-for="s in services" :key="s.name" :app="app" :service="s" />
      </div>
    </section>

    <div v-if="dep" class="flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-card px-4 py-3 text-sm shadow-card">
      <p class="min-w-0 text-muted-foreground">
        <span class="font-medium text-foreground">{{ versionLabel(dep.seq) }}</span> is live, deployed <RelativeTime :date="dep.finished_at ?? dep.queued_at" />.
        You can go back to an earlier version.
      </p>
      <RouterLink :to="{ name: 'app-deployments', params: { slug: app.slug } }" class="inline-flex items-center gap-1 text-xs font-medium hover:underline">
        All deployments <ArrowRight class="size-3.5" />
      </RouterLink>
    </div>
  </div>
</template>
