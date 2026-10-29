<script setup lang="ts">
import { computed } from 'vue'
import type { Tone } from '@/lib/format'
import { cn } from '@/lib/utils'

const props = defineProps<{ label: string; tone: Tone; pulse?: boolean }>()

const toneClass: Record<Tone, string> = {
  success: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-400',
  warning: 'bg-amber-500/15 text-amber-700 dark:text-amber-400',
  danger: 'bg-red-500/15 text-red-700 dark:text-red-400',
  info: 'bg-sky-500/15 text-sky-700 dark:text-sky-400',
  muted: 'bg-muted text-muted-foreground',
}
const dotClass: Record<Tone, string> = {
  success: 'bg-emerald-500',
  warning: 'bg-amber-500',
  danger: 'bg-red-500',
  info: 'bg-sky-500',
  muted: 'bg-muted-foreground/60',
}
const animate = computed(() => props.pulse ?? props.tone === 'info')
</script>

<template>
  <span :class="cn('inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap', toneClass[tone])">
    <span :class="cn('size-1.5 rounded-full', dotClass[tone], animate && 'animate-pulse')" />
    {{ label }}
  </span>
</template>
