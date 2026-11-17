<script setup lang="ts">
// The builds that run or wait, in a card at the bottom of the sidebar, only
// while there are any. Usage is on Home. Hidden while the sidebar is collapsed
// to icons and on mobile.
import { computed } from 'vue'
import BuildsCard from '@/components/BuildsCard.vue'
import { useSidebar } from '@/components/ui/sidebar'
import { useBuilds } from '@/composables/queries'

const { isMobile, state } = useSidebar()
const builds = useBuilds(() => !isMobile.value)
const building = computed(() => !isMobile.value && state.value === 'expanded' && (builds.data.value?.running.length ?? 0) + (builds.data.value?.queued.length ?? 0) > 0)
</script>

<template>
  <section v-if="building" aria-label="Builds" class="relative z-30 w-max max-w-[min(22rem,calc(100vw-1rem))] min-w-full rounded-lg border bg-card text-xs shadow-card" data-testid="status-dock">
    <BuildsCard />
  </section>
</template>
