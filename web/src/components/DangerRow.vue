<script setup lang="ts">
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <div class="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 p-4">
    <div class="min-w-0 max-w-xl">
      <p class="text-sm font-medium">{{ title }}</p>
      <p v-if="description || $slots.description" class="mt-0.5 text-sm text-muted-foreground"><slot name="description">{{ description }}</slot></p>
    </div>
    <div class="flex shrink-0 flex-wrap items-center gap-2"><slot /></div>
  </div>
</template>
