<script setup lang="ts">
import { Box } from 'lucide-vue-next'
import ThemeToggle from './ThemeToggle.vue'

defineProps<{ title: string; description?: string }>()
</script>

<template>
  <div class="relative flex min-h-svh flex-col items-center justify-center gap-6 bg-muted/30 p-4">
    <div class="absolute top-3 right-3"><ThemeToggle /></div>
    <div class="flex items-center gap-2 text-lg font-semibold">
      <div class="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
        <Box class="size-4" />
      </div>
      bitploy
    </div>
    <div class="w-full max-w-sm rounded-xl border bg-card p-6 text-card-foreground shadow-sm">
      <h1 class="text-xl font-semibold tracking-tight">{{ title }}</h1>
      <p v-if="description" class="mt-1 mb-4 text-sm text-muted-foreground">{{ description }}</p>
      <div v-else class="mb-4" />
      <slot />
    </div>
  </div>
</template>
