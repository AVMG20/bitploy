<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import QueryError from '@/components/QueryError.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { formatCount } from '@/lib/format'

export interface TrafficRow {
  key: string
  label: string
  count: number
  /** Shown before the label in small capitals, for example a country code. */
  prefix?: string
  /** A name that is not a real value, like "Direct / none": shown muted. */
  muted?: boolean
}

const props = defineProps<{
  title: string
  unit: string
  rows: TrafficRow[]
  empty: string
  /** There may be more rows than shown (the server sent a full top 10). */
  more?: boolean
  /** Loads the whole list, for "Show all". */
  fetchAll?: () => Promise<TrafficRow[]>
  /** Under the title in the full list, for example the window. */
  description?: string
}>()

const top = computed(() => props.rows.reduce((m, r) => Math.max(m, r.count), 0))
const width = (count: number, max: number) => (max > 0 ? Math.max(2, (count / max) * 100) : 0)

const canShowAll = computed(() => !!props.fetchAll && props.more)

const open = ref(false)
const all = ref<TrafficRow[] | null>(null)
const loading = ref(false)
const error = ref<unknown>(null)
const allTop = computed(() => (all.value ?? []).reduce((m, r) => Math.max(m, r.count), 0))

async function load() {
  if (!props.fetchAll) return
  loading.value = true
  error.value = null
  try {
    all.value = await props.fetchAll()
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}

// Fetched each time the dialog opens, so it matches the numbers on the page.
watch(open, (o) => {
  if (o) void load()
  else all.value = null
})
</script>

<template>
  <Card>
    <CardHeader class="flex flex-row items-baseline justify-between gap-2">
      <CardTitle>{{ title }}</CardTitle>
      <span class="text-xs text-muted-foreground">{{ unit }}</span>
    </CardHeader>
    <CardContent>
      <p v-if="rows.length === 0" class="py-6 text-center text-sm text-muted-foreground">{{ empty }}</p>
      <ul v-else class="grid grid-cols-[minmax(0,1fr)] gap-1">
        <li v-for="r in rows" :key="r.key" class="relative flex items-center justify-between gap-3 rounded-md px-2 py-1 text-sm">
          <span class="absolute inset-y-0 left-0 rounded-md bg-primary/10" :style="{ width: `${width(r.count, top)}%` }" aria-hidden="true" />
          <span class="relative flex min-w-0 items-center gap-2" :title="r.label">
            <span v-if="r.prefix" class="w-5 shrink-0 font-mono text-[11px] text-muted-foreground" aria-hidden="true">{{ r.prefix }}</span>
            <span :class="['truncate', r.muted && 'text-muted-foreground']">{{ r.label }}</span>
          </span>
          <span class="relative shrink-0 font-medium tabular-nums">{{ formatCount(r.count) }}</span>
        </li>
      </ul>
      <Button v-if="canShowAll" variant="ghost" size="sm" class="mt-2 w-full text-muted-foreground" @click="open = true">Show all</Button>
    </CardContent>
  </Card>

  <Dialog v-if="fetchAll" v-model:open="open">
    <DialogContent class="flex max-h-[85vh] flex-col sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>
          {{ description ? `${description}. ` : '' }}{{ unit }} per row{{ all && all.length >= 500 ? ', top 500' : '' }}.
        </DialogDescription>
      </DialogHeader>
      <QueryError v-if="error" :error="error" @retry="load" />
      <div v-else-if="loading && !all" class="grid gap-2">
        <Skeleton v-for="i in 6" :key="i" class="h-7 w-full" />
      </div>
      <ul v-else-if="all" class="-mx-2 grid min-h-0 grid-cols-[minmax(0,1fr)] gap-1 overflow-y-auto px-2">
        <li v-if="all.length === 0" class="py-6 text-center text-sm text-muted-foreground">{{ empty }}</li>
        <li v-for="r in all" :key="r.key" class="relative flex items-center justify-between gap-3 rounded-md px-2 py-1 text-sm">
          <span class="absolute inset-y-0 left-0 rounded-md bg-primary/10" :style="{ width: `${width(r.count, allTop)}%` }" aria-hidden="true" />
          <span class="relative flex min-w-0 items-center gap-2" :title="r.label">
            <span v-if="r.prefix" class="w-5 shrink-0 font-mono text-[11px] text-muted-foreground" aria-hidden="true">{{ r.prefix }}</span>
            <span :class="['truncate', r.muted && 'text-muted-foreground']">{{ r.label }}</span>
          </span>
          <span class="relative shrink-0 font-medium tabular-nums">{{ formatCount(r.count) }}</span>
        </li>
      </ul>
    </DialogContent>
  </Dialog>
</template>
