<script setup lang="ts">
import { computed } from 'vue'
import { Checkbox } from '@/components/ui/checkbox'
import { emergencyMark, groupEvents, toggleEvent } from '@/lib/notifications'
import type { NotificationChannelType, NotificationEvent } from '@/lib/types'

const props = defineProps<{ events: NotificationEvent[]; type: NotificationChannelType; idPrefix: string }>()
const selected = defineModel<string[]>({ required: true })
const groups = computed(() => groupEvents(props.events))
const mark = computed(() => emergencyMark(props.type))

function set(type: string, on: boolean) {
  selected.value = toggleEvent(selected.value, type, on, props.events)
}
</script>

<template>
  <fieldset class="grid gap-4">
    <legend class="mb-2 text-sm font-medium">Events</legend>
    <p v-if="type === 'discord'" class="-mt-2 text-sm text-muted-foreground">Each message pings @here. Emergencies ping @everyone.</p>
    <div v-for="g in groups" :key="g.group" class="grid gap-1">
      <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">{{ g.label }}</p>
      <label
        v-for="e in g.events"
        :key="e.type"
        :for="`${idPrefix}-${e.type}`"
        class="flex cursor-pointer items-start gap-3 rounded-md px-2 py-1.5 hover:bg-muted/50"
      >
        <Checkbox
          :id="`${idPrefix}-${e.type}`"
          class="mt-0.5"
          :model-value="selected.includes(e.type)"
          @update:model-value="(v) => set(e.type, v === true)"
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
  </fieldset>
</template>
