<script setup lang="ts">
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <section class="grid gap-x-10 gap-y-4 md:grid-cols-[15rem_minmax(0,1fr)]">
    <div>
      <h2 class="text-sm font-semibold">{{ title }}</h2>
      <p v-if="description" class="mt-1 text-sm text-muted-foreground">{{ description }}</p>
      <div v-if="$slots.aside" class="mt-3"><slot name="aside" /></div>
    </div>
    <div class="min-w-0"><slot /></div>
  </section>
</template>
