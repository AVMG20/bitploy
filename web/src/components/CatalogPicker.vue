<script setup lang="ts">
import { computed } from 'vue'
import { ChevronRight } from 'lucide-vue-next'
import CatalogMonogram from '@/components/CatalogMonogram.vue'
import QueryError from '@/components/QueryError.vue'
import { Skeleton } from '@/components/ui/skeleton'
import { catalogMatches } from '@/components/catalog/catalogFilter'
import { useCatalog } from '@/composables/queries'
import { formatMB } from '@/lib/format'

// The ready-made apps, as the fourth answer to "Where is the code?". Each one
// opens its own page; the name typed so far goes along.
const props = defineProps<{ name?: string; slug?: string; domain?: string; project?: string; hideIntro?: boolean; query?: string; category?: string }>()
const catalog = useCatalog()
// The Ready-made page's search and category chips narrow the list.
const shown = computed(() => (catalog.data.value ?? []).filter((c) => catalogMatches(c, props.query ?? '', props.category ?? '')))
</script>

<template>
  <div class="grid gap-3">
    <p v-if="!hideIntro" class="text-xs text-muted-foreground">Well-known apps bitploy sets up for you. Pick one to see what it needs.</p>
    <QueryError v-if="catalog.isError.value" :error="catalog.error.value" @retry="catalog.refetch()" />
    <div v-else class="grid gap-2 sm:grid-cols-2">
      <template v-if="catalog.isPending.value">
        <Skeleton v-for="i in 4" :key="i" class="h-16 rounded-lg" />
      </template>
      <RouterLink
        v-for="c in shown"
        v-else
        :key="c.id"
        :to="{ name: 'new-catalog', params: { catalogId: c.id }, query: name?.trim() || domain || project ? { name: name?.trim() || undefined, slug: slug || undefined, domain: domain || undefined, project: project || undefined } : undefined }"
        class="group flex min-w-0 items-center gap-3 rounded-lg border bg-card p-3 transition-colors hover:bg-muted/40 focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
      >
        <CatalogMonogram :name="c.name" :id="c.id" size="sm" />
        <span class="grid min-w-0 flex-1">
          <span class="truncate text-sm font-medium">{{ c.name }}</span>
          <span class="truncate text-xs text-muted-foreground" :title="c.tagline">{{ c.tagline }}</span>
          <span class="text-[11px] text-muted-foreground tabular-nums">Up to {{ formatMB(c.memory_mb) }} memory</span>
        </span>
        <ChevronRight class="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
      </RouterLink>
      <p v-if="catalog.data.value && !catalog.data.value.length" class="text-sm text-muted-foreground sm:col-span-2">No ready-made apps are available.</p>
      <p v-else-if="catalog.data.value && !shown.length" class="text-sm text-muted-foreground sm:col-span-2">No app matches. Try another word or category.</p>
    </div>
  </div>
</template>
