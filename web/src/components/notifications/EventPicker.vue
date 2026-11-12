<script setup lang="ts">
import { computed } from 'vue'
import EventGroupRows from './EventGroupRows.vue'
import { emergencyMark, splitAdminEvents, toggleEvent } from '@/lib/notifications'
import type { NotificationChannelType, NotificationEvent } from '@/lib/types'

const props = defineProps<{ events: NotificationEvent[]; type: NotificationChannelType; idPrefix: string }>()
const selected = defineModel<string[]>({ required: true })
const sections = computed(() => splitAdminEvents(props.events))
const mark = computed(() => emergencyMark(props.type))

function set(type: string, on: boolean) {
  selected.value = toggleEvent(selected.value, type, on, props.events)
}
</script>

<template>
  <fieldset class="grid gap-4">
    <legend class="mb-2 text-sm font-medium">Events</legend>
    <p v-if="type === 'discord'" class="-mt-2 text-sm text-muted-foreground">Emergencies ping @here. Other messages ping nobody.</p>
    <!-- Admins get two labelled sections, so it is clear what is about their own apps and what about the server. -->
    <template v-if="sections.admin.length">
      <section class="grid gap-4 rounded-md border p-3">
        <div class="grid gap-0.5">
          <p class="text-sm font-medium">Your apps</p>
          <p class="text-xs text-muted-foreground">Deployments, backups and crashes of your apps and the projects shared with you.</p>
        </div>
        <EventGroupRows v-for="g in sections.own" :key="g.group" :group="g" :selected="selected" :mark="mark" :id-prefix="idPrefix" @toggle="set" />
      </section>
      <section class="grid gap-4 rounded-md border bg-muted/30 p-3">
        <div class="grid gap-0.5">
          <p class="text-sm font-medium">Server (admin)</p>
          <p class="text-xs text-muted-foreground">Only admins see these. They are about the whole server, not your own apps.</p>
        </div>
        <EventGroupRows v-for="g in sections.admin" :key="g.group" :group="g" :selected="selected" :mark="mark" :id-prefix="idPrefix" @toggle="set" />
      </section>
    </template>
    <EventGroupRows v-for="g in sections.own" v-else :key="g.group" :group="g" :selected="selected" :mark="mark" :id-prefix="idPrefix" @toggle="set" />
  </fieldset>
</template>
