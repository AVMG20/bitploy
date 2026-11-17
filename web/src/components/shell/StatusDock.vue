<script setup lang="ts">
// One card at the bottom of the sidebar: the builds that run or wait on top
// (only while there are any) and, below them, how much of the limits the apps
// take. Clicking the meters opens Home, where the details are. Hidden while the
// sidebar is collapsed to icons; builds are not shown on mobile (as before),
// the meters are.
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { Infinity as InfinityIcon } from 'lucide-vue-next'
import BuildsCard from '@/components/BuildsCard.vue'
import { useServerUsage } from '@/components/home/useServerUsage'
import MeterBar from './MeterBar.vue'
import { useSidebar } from '@/components/ui/sidebar'
import { useBuilds } from '@/composables/queries'

const { isMobile, state, setOpenMobile } = useSidebar()
const { meters, memAlert, appsAlert } = useServerUsage()
const builds = useBuilds(() => !isMobile.value)
const building = computed(() => !isMobile.value && (builds.data.value?.running.length ?? 0) + (builds.data.value?.queued.length ?? 0) > 0)
const visible = computed(() => (isMobile.value || state.value === 'expanded') && (building.value || meters.value.length > 0))
const alert = computed(() => memAlert.value || appsAlert.value)
</script>

<template>
  <section v-if="visible" aria-label="Builds and usage" class="relative z-30 w-max max-w-[min(22rem,calc(100vw-1rem))] min-w-full rounded-lg border bg-card text-xs shadow-card" data-testid="status-dock">
    <BuildsCard v-if="building" />
    <RouterLink
      v-if="meters.length"
      to="/"
      :class="['grid gap-2 p-2.5 transition-colors hover:bg-accent/50 focus-visible:bg-accent/50 focus-visible:outline-none', building ? 'rounded-b-lg border-t' : 'rounded-lg']"
      :title="alert || 'Open Home for the details'"
      @click="setOpenMobile(false)"
    >
      <div v-for="m in meters" :key="m.key" class="grid gap-1">
        <div class="flex items-center justify-between gap-2">
          <span class="text-muted-foreground">{{ m.label }}</span>
          <span class="inline-flex items-center gap-1 font-mono text-[11px] text-foreground tabular-nums">
            {{ m.text }}<InfinityIcon v-if="!m.hasMax" class="size-3 text-muted-foreground" aria-label="no limit" />
          </span>
        </div>
        <MeterBar v-if="m.hasMax" :pct="m.pct" :level="m.bar.level" :label="`${m.label}: ${m.text}`" />
      </div>
    </RouterLink>
  </section>
</template>
