<script setup lang="ts">
import { computed } from 'vue'
import { appStatusInfo } from '@/lib/format'
import { cn } from '@/lib/utils'

// A small coloured dot for an app's status where a badge would not fit (chips,
// lists). The label is there for screen readers and on hover.
const props = defineProps<{ status: string }>()
const info = computed(() => appStatusInfo(props.status))
const DOT = { success: 'bg-success', warning: 'bg-warning', danger: 'bg-destructive', info: 'bg-info', muted: 'bg-muted-foreground/50' }
</script>

<template>
  <span class="relative inline-flex size-2 shrink-0" :title="info.label">
    <span v-if="info.tone === 'info'" aria-hidden="true" :class="cn('absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:hidden', DOT[info.tone])" />
    <span :class="cn('relative inline-flex size-2 rounded-full', DOT[info.tone])" />
    <span class="sr-only">{{ info.label }}</span>
  </span>
</template>
