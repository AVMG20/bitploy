<script setup lang="ts">
import { RouterLink } from 'vue-router'
import CatalogMonogram from '@/components/CatalogMonogram.vue'
import { useCatalog } from '@/composables/queries'
import { formatMB } from '@/lib/format'

// Left out when the list cannot be loaded or is empty: it is only a shortcut.
const catalog = useCatalog()
</script>

<template>
  <section v-if="catalog.data.value?.length" class="grid gap-3">
    <div>
      <h2 class="text-sm font-semibold">Start from a one-click app</h2>
      <p class="mt-1 text-sm text-muted-foreground">Ready-made apps with the Dockerfile already written. You see what you get before anything is deployed.</p>
    </div>
    <div class="grid gap-3 sm:grid-cols-2">
      <RouterLink
        v-for="c in catalog.data.value"
        :key="c.id"
        :to="{ name: 'new-catalog', params: { catalogId: c.id } }"
        class="flex min-w-0 items-start gap-3 rounded-xl border bg-card p-3 shadow-card transition-colors hover:bg-muted/40 focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
      >
        <CatalogMonogram :name="c.name" />
        <div class="min-w-0 flex-1">
          <p class="truncate text-sm font-medium">{{ c.name }}</p>
          <p class="line-clamp-2 text-xs text-muted-foreground">{{ c.tagline }}</p>
          <p class="mt-1.5 font-mono text-[11px] text-muted-foreground">{{ c.version }} · {{ formatMB(c.memory_mb) }}</p>
        </div>
      </RouterLink>
    </div>
  </section>
</template>
