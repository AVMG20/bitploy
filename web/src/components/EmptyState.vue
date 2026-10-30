<script setup lang="ts">
import type { Component } from 'vue'

defineProps<{ title: string; description?: string; icon?: Component }>()
</script>

<template>
  <div class="flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed bg-card/60 px-6 py-14 text-center">
    <div v-if="icon" class="mb-1 flex size-10 items-center justify-center rounded-lg border bg-card text-muted-foreground shadow-card">
      <component :is="icon" class="size-5" />
    </div>
    <p class="font-medium">{{ title }}</p>
    <p v-if="description" class="max-w-md text-sm text-muted-foreground">{{ description }}</p>
    <div class="mt-2 flex flex-wrap justify-center gap-2"><slot /></div>
  </div>
</template>
