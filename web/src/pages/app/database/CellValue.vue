<script setup lang="ts">
import { computed } from 'vue'
import { cellView } from '@/lib/dbvalue'
import type { DbValue } from '@/lib/types'

// What one value looks like in a cell. It is only text: opening the full value is the grid's expand button.
const props = defineProps<{ value: DbValue }>()
const view = computed(() => cellView(props.value))
</script>

<template>
  <span v-if="view.kind === 'null'" class="rounded bg-muted px-1.5 py-px font-sans text-[11px] font-medium text-muted-foreground italic">NULL</span>
  <span v-else-if="value === ''" class="rounded bg-muted px-1.5 py-px font-sans text-[11px] text-muted-foreground/80 italic">empty</span>
  <span v-else :class="[view.kind === 'int' || view.kind === 'real' ? 'tabular-nums' : '', view.kind === 'blob' && 'text-muted-foreground']">{{ view.text }}</span>
</template>
