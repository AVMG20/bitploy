<script setup lang="ts">
import { ref } from 'vue'
import { ArrowDown, ArrowUp, Check, KeyRound, Lock, Maximize2, Minus } from 'lucide-vue-next'
import { Checkbox } from '@/components/ui/checkbox'
import { useFillHeight } from '@/composables/useFillHeight'
import { cellView } from '@/lib/dbvalue'
import type { DbValue } from '@/lib/types'
import CellValue from './CellValue.vue'

// A grid of values with a sticky header. It scrolls inside its own box, so the
// page itself never scrolls sideways. Used for table rows and for SQL results.
const props = withDefaults(
  defineProps<{
    columns: string[]
    rows: DbValue[][]
    /** One id per row; with `selectable`, a checkbox column. */
    rowIds?: string[]
    selectable?: boolean
    selected?: Set<string>
    sortable?: boolean
    orderBy?: string
    desc?: boolean
    /** Columns whose values the server hides: a small lock pill instead of the value. */
    redacted?: string[]
    /** Columns of the primary key get a small key icon. */
    keyColumns?: string[]
    /** Can this cell be edited in place? (double-click) */
    cellEditable?: (row: number, col: number) => boolean
    /** The cell that is being edited now: the `editor` slot shows in its place. */
    editing?: { row: number; col: number } | null
    /** The cell whose new value is on its way to the server. */
    savingCell?: { row: number; col: number } | null
    /** The cell that was just saved: it flashes. */
    flashCell?: { row: number; col: number } | null
    /** Grow to the bottom of the screen, leaving this many pixels for what follows (the grid then scrolls inside). */
    reserve?: number
    dim?: boolean
  }>(),
  { reserve: 96 },
)
const root = ref<HTMLElement | null>(null)
const { maxHeight } = useFillHeight(root, props.reserve)
const emit = defineEmits<{
  sort: [column: string]
  toggle: [id: string]
  toggleAll: [checked: boolean]
  cellDblclick: [row: number, col: number]
  view: [value: DbValue, column: string, row: number]
}>()
</script>

<template>
  <div ref="root" class="overflow-auto rounded-lg border bg-card transition-opacity" :class="dim && 'opacity-60'" :style="{ maxHeight }">
    <table class="w-max min-w-full border-separate border-spacing-0 text-[13px]">
      <thead>
        <tr>
          <th v-if="selectable" class="sticky top-0 z-10 h-9 w-9 border-b bg-muted px-3 text-left">
            <Checkbox
              aria-label="Select all rows"
              :model-value="!!rows.length && selected?.size === rows.length ? true : selected?.size ? 'indeterminate' : false"
              class="data-[state=indeterminate]:border-primary data-[state=indeterminate]:bg-primary data-[state=indeterminate]:text-primary-foreground"
              @update:model-value="(v) => emit('toggleAll', v === true)"
            >
              <template #default="{ modelValue }"><Minus v-if="modelValue === 'indeterminate'" /><Check v-else /></template>
            </Checkbox>
          </th>
          <th
            v-for="c in columns"
            :key="c"
            class="sticky top-0 z-10 h-9 border-b bg-muted px-3 text-left text-xs font-medium whitespace-nowrap text-muted-foreground"
            :aria-sort="orderBy === c ? (desc ? 'descending' : 'ascending') : undefined"
          >
            <button v-if="sortable && !redacted?.includes(c)" type="button" class="-mx-1 inline-flex items-center gap-1 rounded px-1 py-0.5 hover:text-foreground" :class="orderBy === c && 'text-foreground'" @click="emit('sort', c)">
              <KeyRound v-if="keyColumns?.includes(c)" class="size-3 text-warning" aria-label="Primary key" />
              {{ c }}
              <component :is="desc ? ArrowDown : ArrowUp" v-if="orderBy === c" class="size-3" />
            </button>
            <span v-else class="inline-flex items-center gap-1">
              <KeyRound v-if="keyColumns?.includes(c)" class="size-3 text-warning" aria-label="Primary key" />
              {{ c }}
            </span>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="(row, i) in rows"
          :key="rowIds?.[i] ?? i"
          class="group/row hover:bg-muted/40"
          :class="rowIds && selected?.has(rowIds[i]!) && 'bg-primary/5'"
        >
          <td v-if="selectable" class="w-9 border-b px-3 py-1.5 align-middle">
            <Checkbox :aria-label="`Select row ${i + 1}`" :model-value="!!rowIds && !!selected?.has(rowIds[i]!)" :disabled="!rowIds?.[i]" @update:model-value="rowIds?.[i] && emit('toggle', rowIds[i]!)" />
          </td>
          <td
            v-for="(c, j) in columns"
            :key="c + j"
            class="group/cell border-b px-3 py-1.5 align-middle font-mono"
            :class="[
              'transition-colors',
              editing?.row === i && editing.col === j && 'relative',
              savingCell?.row === i && savingCell.col === j && 'opacity-50',
              flashCell?.row === i && flashCell.col === j && 'bg-success/20',
              cellEditable?.(i, j) && !(editing?.row === i && editing.col === j) && 'cursor-text hover:bg-muted/60 hover:shadow-[inset_0_0_0_1px_var(--border)]',
            ]"
            :title="cellEditable?.(i, j) && !(editing?.row === i && editing.col === j) ? 'Double-click to edit' : undefined"
            @dblclick="emit('cellDblclick', i, j)"
          >
            <slot v-if="editing?.row === i && editing.col === j" name="editor" :row="i" :col="j" />
            <span v-else-if="redacted?.includes(c)" class="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-px font-sans text-[11px] font-medium text-muted-foreground" title="The server does not show this value">
              <Lock class="size-3" /> hidden
            </span>
            <div v-else class="flex max-w-[22rem] items-center gap-1">
              <CellValue class="min-w-0 truncate" :value="row[j] ?? null" />
              <button
                v-if="cellView(row[j] ?? null).expandable"
                type="button"
                class="inline-flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground opacity-0 group-hover/cell:opacity-100 hover:bg-muted hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
                aria-label="See the whole value"
                title="See the whole value"
                @click="emit('view', row[j] ?? null, c, i)"
              >
                <Maximize2 class="size-3" />
              </button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
