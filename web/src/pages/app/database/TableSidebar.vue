<script setup lang="ts">
import { computed, ref } from 'vue'
import { Eye, Search, Table2 } from 'lucide-vue-next'
import { Input } from '@/components/ui/input'
import { formatCount } from '@/lib/format'
import type { DbTable } from '@/lib/types'

const props = defineProps<{ tables: DbTable[]; selected: string | undefined }>()
const emit = defineEmits<{ select: [name: string] }>()

const query = ref('')
const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  return q ? props.tables.filter((t) => t.name.toLowerCase().includes(q)) : props.tables
})
const groups = computed(() =>
  [
    { label: 'Tables', items: shown.value.filter((t) => t.type === 'table') },
    { label: 'Views', items: shown.value.filter((t) => t.type === 'view') },
  ].filter((g) => g.items.length),
)
const hasViews = computed(() => props.tables.some((t) => t.type === 'view'))
</script>

<template>
  <div class="flex min-h-0 flex-col gap-2 rounded-lg border bg-card p-2">
    <div class="relative">
      <Search class="pointer-events-none absolute top-2 left-2 size-4 text-muted-foreground" />
      <Input v-model="query" class="pl-8" placeholder="Search tables" aria-label="Search tables and views" autocomplete="off" spellcheck="false" />
    </div>
    <nav class="-mx-0.5 max-h-60 overflow-x-hidden overflow-y-auto px-0.5 md:max-h-[calc(100svh-15rem)]" aria-label="Tables and views">
      <p v-if="!tables.length" class="px-2 py-3 text-sm text-muted-foreground">This database has no tables.</p>
      <p v-else-if="!shown.length" class="px-2 py-3 text-sm text-muted-foreground">Nothing matches.</p>
      <div v-for="g in groups" :key="g.label" class="mb-1 last:mb-0">
        <p v-if="hasViews" class="px-2 pt-1.5 pb-1 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{{ g.label }}</p>
        <ul class="grid grid-cols-[minmax(0,1fr)] gap-px">
          <li v-for="t in g.items" :key="t.name">
            <button
              type="button"
              class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
              :class="selected === t.name ? 'bg-muted font-medium' : 'text-foreground/80'"
              :aria-current="selected === t.name ? 'true' : undefined"
              @click="emit('select', t.name)"
            >
              <component :is="t.type === 'view' ? Eye : Table2" class="size-4 shrink-0 text-muted-foreground" />
              <span class="min-w-0 flex-1 truncate font-mono text-[13px]" :title="t.name">{{ t.name }}</span>
              <span class="shrink-0 text-xs text-muted-foreground tabular-nums" :title="t.rows === null ? 'Counting took too long' : `${t.rows} rows`">{{ t.rows === null ? '?' : formatCount(t.rows) }}</span>
            </button>
          </li>
        </ul>
      </div>
    </nav>
  </div>
</template>
