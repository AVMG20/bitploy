<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: HTMLAttributes['class'] }>()
</script>

<template>
  <!-- A box (one app, one container) inside the hexagon shared by our apps. -->
  <svg
    viewBox="0 0 100 100"
    aria-hidden="true"
    :class="cn('size-7 shrink-0 text-foreground', props.class)"
  >
    <polygon
      points="50,8 86.4,29 86.4,71 50,92 13.6,71 13.6,29"
      fill="none"
      stroke="currentColor"
      stroke-width="7"
    />
    <g fill="none" stroke="currentColor" stroke-width="4.5">
      <polygon points="50,31 66.5,40.5 66.5,59.5 50,69 33.5,59.5 33.5,40.5" />
      <path d="M50 50V69M50 50L33.5 40.5M50 50L66.5 40.5" />
    </g>
  </svg>
</template>
