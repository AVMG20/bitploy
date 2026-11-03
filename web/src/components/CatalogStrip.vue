<script setup lang="ts">
import { RouterLink } from 'vue-router'
import CatalogMonogram from '@/components/CatalogMonogram.vue'
import { Skeleton } from '@/components/ui/skeleton'
import { useCatalog } from '@/composables/queries'

// A shortcut above the form: one row of chips that scrolls sideways on a
// narrow screen. Left out when the list cannot be loaded or is empty.
const catalog = useCatalog()
</script>

<template>
  <section v-if="catalog.isPending.value || catalog.data.value?.length" class="grid min-w-0 gap-2" aria-labelledby="one-click-title">
    <h2 id="one-click-title" class="text-xs font-medium text-muted-foreground">Or start from a one-click app</h2>
    <div class="-mx-1 flex gap-2 overflow-x-auto px-1 pb-1 [scrollbar-width:thin] sm:flex-wrap sm:overflow-visible">
      <template v-if="catalog.isPending.value">
        <Skeleton v-for="i in 4" :key="i" class="h-9 w-32 shrink-0 rounded-lg" />
      </template>
      <RouterLink
        v-for="c in catalog.data.value"
        v-else
        :key="c.id"
        :to="{ name: 'new-catalog', params: { catalogId: c.id } }"
        :title="c.tagline"
        class="flex h-9 shrink-0 items-center gap-2 rounded-lg border bg-card pr-3 pl-1.5 text-sm font-medium shadow-card transition-colors hover:bg-muted/40 focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
      >
        <CatalogMonogram :name="c.name" size="xs" />
        {{ c.name }}
      </RouterLink>
    </div>
  </section>
</template>
