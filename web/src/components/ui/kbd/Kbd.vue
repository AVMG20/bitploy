<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: HTMLAttributes['class'] }>()
</script>

<template>
  <kbd
    data-slot="kbd"
    :class="cn('pointer-events-none inline-flex h-5 min-w-5 items-center justify-center gap-0.5 rounded border bg-muted px-1 font-sans text-[11px] font-medium text-muted-foreground select-none', props.class)"
  >
    <slot />
  </kbd>
</template>
