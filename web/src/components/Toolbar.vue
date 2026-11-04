<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'

/**
 * A row of controls above a list or a log: search, filters, view switches and
 * actions. Everything in it is one height (h-8, the default Button, Input and
 * Select size), so a row never mixes sizes. Use default-size buttons (and
 * size="icon" for icon-only ones) inside it; the rules below are the guard.
 */
const props = defineProps<{ class?: HTMLAttributes['class'] }>()
</script>

<template>
  <div
    data-slot="toolbar"
    :class="cn(
      'flex flex-wrap items-center gap-2',
      '[&_[data-slot=button]]:h-8 [&_[data-slot=input]]:h-8 [&_[data-slot=search-input]]:h-8 [&_[data-slot=select-trigger]]:h-8 [&_[data-slot=tabs-list]]:h-8',
      props.class,
    )"
  >
    <slot />
  </div>
</template>
