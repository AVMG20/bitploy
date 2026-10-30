<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: HTMLAttributes['class'] }>()
</script>

<template>
  <svg
    viewBox="0 0 32 32"
    aria-hidden="true"
    :class="cn('size-7 shrink-0 text-primary', props.class)"
  >
    <rect width="32" height="32" rx="8" fill="currentColor" />
    <path
      d="M10.5 22V10h6a3.6 3.6 0 0 1 0 7.2h-6m6 0a2.4 2.4 0 0 1 0 4.8h-6"
      fill="none"
      stroke="var(--color-primary-foreground)"
      stroke-width="2.4"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  </svg>
</template>
