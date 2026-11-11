<script setup lang="ts">
import { computed } from 'vue'
import { appStatusInfo } from '@/lib/format'
import type { BuildPhase } from '@/composables/queries'
import { cn } from '@/lib/utils'

// A small coloured dot for an app's status where a badge would not fit (chips,
// lists). The label is there for screen readers and on hover. While a new
// version builds, a ring in the same colour turns around the dot: green while
// the live version keeps serving, red when it was down. A dashed, still ring:
// the new version waits in line. A restart or start turns the ring too, as
// the app is down until its containers are back.
const props = defineProps<{ status: string; building?: BuildPhase }>()
const info = computed(() => appStatusInfo(props.status))
const bringing = computed(() => props.status === 'restarting' || props.status === 'starting')
const ring = computed<BuildPhase | undefined>(() => props.building ?? (bringing.value ? 'running' : undefined))
const label = computed(() => (props.building === 'running' ? `${info.value.label}, building a new version` : props.building === 'queued' ? `${info.value.label}, a new version waits to build` : info.value.label))
const DOT = { success: 'bg-success', warning: 'bg-warning', danger: 'bg-destructive', progress: 'bg-foreground', muted: 'bg-muted-foreground/50' }
const RING = { success: 'border-success', warning: 'border-warning', danger: 'border-destructive', progress: 'border-foreground', muted: 'border-muted-foreground' }
</script>

<template>
  <span class="relative inline-flex size-2 shrink-0" :title="label">
    <span
      v-if="ring"
      aria-hidden="true"
      :class="cn(
        'absolute -inset-[3px] rounded-full border-[1.5px]',
        RING[info.tone],
        ring === 'running' ? 'border-t-transparent animate-spin [animation-duration:900ms] motion-reduce:animate-none' : 'border-dashed opacity-60',
      )"
    />
    <span :class="cn('inline-flex size-2 rounded-full', DOT[info.tone], info.tone === 'progress' && !ring && 'status-pulse')" />
    <span class="sr-only">{{ label }}</span>
  </span>
</template>
