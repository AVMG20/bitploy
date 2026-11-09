<script setup lang="ts">
import { computed } from 'vue'
import { logoFor } from '@/lib/logos'

// The tile in front of a ready-made app or service: its logo, or the first
// letter of its name when there is none.
const props = defineProps<{ name: string; id?: string; size?: 'xs' | 'sm' | 'lg' }>()
const logo = computed(() => (props.id ? logoFor(props.id) : null))
</script>

<template>
  <span
    aria-hidden="true"
    :class="[
      'flex shrink-0 items-center justify-center rounded-lg border bg-muted font-semibold text-foreground shadow-card',
      size === 'lg' ? 'size-12 text-xl' : size === 'xs' ? 'size-6 rounded-md text-xs shadow-none' : 'size-9 text-base',
    ]"
  >
    <svg v-if="logo" viewBox="0 0 24 24" :class="size === 'lg' ? 'size-6' : size === 'xs' ? 'size-3.5' : 'size-5'" :fill="logo.color ?? 'currentColor'"><path :d="logo.path" /></svg>
    <template v-else>{{ name.charAt(0).toUpperCase() }}</template>
  </span>
</template>
