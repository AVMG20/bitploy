<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { ArrowDown, ArrowUp, ChevronsUpDown } from 'lucide-vue-next'
import { TableHead } from '@/components/ui/table'
import { cn } from '@/lib/utils'

// A column heading that sorts the table when clicked.
const props = defineProps<{ label: string; active: boolean; desc: boolean; align?: 'left' | 'right'; class?: HTMLAttributes['class'] }>()
const emit = defineEmits<{ sort: [] }>()
</script>

<template>
  <TableHead :class="props.class" :aria-sort="active ? (desc ? 'descending' : 'ascending') : 'none'">
    <button
      type="button"
      :class="cn(
        'group/sort -mx-1.5 inline-flex h-7 items-center gap-1 rounded px-1.5 uppercase transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none',
        active && 'text-foreground',
        align === 'right' && 'flex-row-reverse',
      )"
      :title="`Sort by ${label.toLowerCase()}`"
      @click="emit('sort')"
    >
      {{ label }}
      <ArrowDown v-if="active && desc" class="size-3.5" aria-hidden="true" />
      <ArrowUp v-else-if="active" class="size-3.5" aria-hidden="true" />
      <ChevronsUpDown v-else class="size-3.5 opacity-0 transition-opacity group-hover/sort:opacity-60 group-focus-visible/sort:opacity-60" aria-hidden="true" />
    </button>
  </TableHead>
</template>
