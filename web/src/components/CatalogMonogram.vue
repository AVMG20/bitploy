<script setup lang="ts">
defineProps<{ name: string; size?: 'sm' | 'lg' }>()
</script>

<template>
  <span
    aria-hidden="true"
    :class="[
      'flex shrink-0 items-center justify-center rounded-lg border bg-muted font-semibold text-foreground shadow-card',
      size === 'lg' ? 'size-12 text-xl' : 'size-9 text-base',
    ]"
  >{{ name.charAt(0).toUpperCase() }}</span>
</template>
