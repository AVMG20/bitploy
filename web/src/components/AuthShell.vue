<script setup lang="ts">
import BrandMark from './BrandMark.vue'
import ThemeToggle from './ThemeToggle.vue'

defineProps<{ title: string; description?: string }>()
</script>

<template>
  <div class="auth-canvas relative flex min-h-svh flex-col items-center justify-center gap-8 p-4">
    <div class="absolute top-3 right-3"><ThemeToggle /></div>
    <div class="flex items-center gap-2.5 text-lg font-semibold tracking-tight">
      <BrandMark class="size-8" />
      bitploy
    </div>
    <div class="w-full max-w-sm rounded-xl border bg-card p-7 text-card-foreground shadow-[0_1px_2px_oklch(0.2_0.02_260/0.04),0_8px_24px_-8px_oklch(0.2_0.02_260/0.1)]">
      <h1 class="text-xl font-semibold tracking-tight">{{ title }}</h1>
      <p v-if="description" class="mt-1 mb-4 text-sm text-muted-foreground">{{ description }}</p>
      <div v-else class="mb-4" />
      <slot />
    </div>
  </div>
</template>

<style scoped>
.auth-canvas {
  background-color: var(--background);
  background-image: radial-gradient(circle at 1px 1px, color-mix(in oklch, var(--foreground) 9%, transparent) 1px, transparent 0);
  background-size: 22px 22px;
}
</style>
