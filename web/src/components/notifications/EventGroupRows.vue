<script setup lang="ts">
import { Checkbox } from '@/components/ui/checkbox'
import type { EventGroup } from '@/lib/notifications'

defineProps<{ group: EventGroup; selected: string[]; mark: string; idPrefix: string }>()
const emit = defineEmits<{ toggle: [type: string, on: boolean] }>()
</script>

<template>
  <div class="grid gap-1">
    <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">{{ group.label }}</p>
    <label
      v-for="e in group.events"
      :key="e.type"
      :for="`${idPrefix}-${e.type}`"
      class="flex cursor-pointer items-start gap-3 rounded-md px-2 py-1.5 hover:bg-muted/50"
    >
      <Checkbox
        :id="`${idPrefix}-${e.type}`"
        class="mt-0.5"
        :model-value="selected.includes(e.type)"
        @update:model-value="(v) => emit('toggle', e.type, v === true)"
      />
      <span class="grid min-w-0 gap-0.5">
        <span class="flex flex-wrap items-center gap-2 text-sm">
          {{ e.label }}
          <span v-if="e.emergency" class="rounded-md border border-destructive/30 bg-destructive/10 px-1.5 text-xs text-destructive">{{ mark }}</span>
        </span>
        <span v-if="e.description" class="text-xs text-muted-foreground">{{ e.description }}</span>
      </span>
    </label>
  </div>
</template>
