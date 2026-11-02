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
  <span
    :class="cn(
      'relative inline-flex h-5.5 items-center gap-1.5 overflow-hidden rounded-md border px-2 text-xs font-medium whitespace-nowrap',
      toneClass[tone],
    )"
  >
    <span v-if="animate" aria-hidden="true" class="badge-shimmer pointer-events-none absolute inset-0 motion-reduce:hidden" />
    <span class="relative flex size-1.5">
      <span
        v-if="animate"
        aria-hidden="true"
        :class="cn('absolute inline-flex size-full animate-ping rounded-full opacity-75 motion-reduce:hidden', dotClass[tone])"
      />
      <span :class="cn('relative inline-flex size-1.5 rounded-full', dotClass[tone])" />
    </span>
    <span class="relative">{{ label }}</span>
  </span>
</template>
