<script setup lang="ts">
import { computed } from 'vue'
import type { Tone } from '@/lib/format'
import { cn } from '@/lib/utils'

const props = defineProps<{ label: string; tone: Tone; pulse?: boolean }>()

const toneClass: Record<Tone, string> = {
  success: 'border-success/25 bg-success/10 text-success',
  warning: 'border-warning/30 bg-warning/10 text-[color-mix(in_oklch,var(--warning)_75%,var(--foreground))]',
  danger: 'border-destructive/25 bg-destructive/10 text-destructive',
  info: 'border-info/25 bg-info/10 text-info',
  muted: 'border-border bg-muted text-muted-foreground',
}
const dotClass: Record<Tone, string> = {
  success: 'bg-success',
  warning: 'bg-warning',
  danger: 'bg-destructive',
  info: 'bg-info',
  muted: 'bg-muted-foreground/50',
}
const animate = computed(() => props.pulse ?? props.tone === 'info')
</script>

<template>
  <span :class="cn('inline-flex h-5.5 items-center gap-1.5 rounded-md border px-2 text-xs font-medium whitespace-nowrap', toneClass[tone])">
    <span :class="cn('size-1.5 rounded-full', dotClass[tone], animate && 'animate-pulse')" />
    {{ label }}
  </span>
</template>
