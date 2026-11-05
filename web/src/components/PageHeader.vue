<script setup lang="ts">
// The one header every page starts with: an optional icon, the title with
// badges next to it, a line of facts or a description under it, and the
// page's actions on the right.
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <!-- From lg up the actions keep their place on the right and the facts wrap instead. -->
  <header class="flex flex-wrap items-start justify-between gap-x-6 gap-y-4 lg:flex-nowrap">
    <div class="flex min-w-0 items-start gap-3 lg:flex-1">
      <div v-if="$slots.icon" class="shrink-0 pt-0.5"><slot name="icon" /></div>
      <div class="min-w-0">
        <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
          <h1 class="min-w-0 truncate text-2xl font-semibold tracking-tight">{{ title }}</h1>
          <slot name="badges" />
        </div>
        <div v-if="$slots.meta" class="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground"><slot name="meta" /></div>
        <p v-else-if="description" class="mt-1 text-sm text-muted-foreground">{{ description }}</p>
      </div>
    </div>
    <div v-if="$slots.default" class="flex flex-wrap items-center gap-2 lg:shrink-0 lg:justify-end"><slot /></div>
  </header>
</template>
