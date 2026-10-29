<script setup lang="ts">
import type { Component } from 'vue'

defineProps<{ title: string; description?: string; icon?: Component }>()
</script>

<template>
  <div class="flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed px-6 py-12 text-center">
    <component :is="icon" v-if="icon" class="size-8 text-muted-foreground" />
    <p class="font-medium">{{ title }}</p>
    <p v-if="description" class="max-w-md text-sm text-muted-foreground">{{ description }}</p>
    <div class="mt-2 flex flex-wrap justify-center gap-2"><slot /></div>
  </div>
</template>
