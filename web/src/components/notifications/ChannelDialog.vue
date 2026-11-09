<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiError, api } from '@/lib/api'
import { defaultEvents, type PushState } from '@/lib/notifications'
import { PushDenied, subscribe } from '@/lib/push'
import { toastError } from '@/lib/notify'
import { cn } from '@/lib/utils'
import type { NotificationChannel, NotificationChannelType, NotificationOptions } from '@/lib/types'
import EventPicker from './EventPicker.vue'
import { CHANNEL_TYPES } from './channelTypes'

const props = defineProps<{
  options: NotificationOptions
  /** The channel to edit; none to add one. */
  channel?: NotificationChannel | null
  /** The type a new channel starts with. */
  startType?: NotificationChannelType
  pushState: PushState
  /** The endpoint hash of this browser's subscription. */
  currentHash?: string | null
  /** This browser has a channel already: it is not offered as a new one. */
  browserAdded?: boolean
}>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ saved: [NotificationChannel] }>()

const type = ref<NotificationChannelType>('discord')
const name = ref('')
const events = ref<string[]>([])
const config = ref<Record<string, unknown>>({})
const errors = ref<{ name?: string; config?: string }>({})
const busy = ref(false)

const editing = computed(() => !!props.channel)
const types = computed(() =>
  props.options.types
    .filter((t) => !(t.type === 'browser' && props.browserAdded))
    .map((t) => CHANNEL_TYPES[t.type])
    .filter(Boolean),
)
const ui = computed(() => CHANNEL_TYPES[type.value])
const current = computed(() => !!props.channel && props.channel.config.endpoint_hash === props.currentHash && !!props.currentHash)
const browserBlocked = computed(() => type.value === 'browser' && !editing.value && props.pushState !== 'default' && props.pushState !== 'granted')

function reset() {
  const c = props.channel
  type.value = c?.type ?? props.startType ?? types.value[0]?.type ?? 'discord'
  name.value = c?.name ?? CHANNEL_TYPES[type.value].defaultName()
  events.value = c ? [...c.events] : defaultEvents(props.options.events)
  config.value = {}
  errors.value = {}
}
watch(open, (v) => v && reset(), { immediate: true })

function pickType(t: NotificationChannelType) {
  if (t === type.value) return
  // A name the user did not type follows the type.
  if (name.value === CHANNEL_TYPES[type.value].defaultName()) name.value = CHANNEL_TYPES[t].defaultName()
  type.value = t
  config.value = {}
  errors.value = {}
}

function check(): boolean {
  const e: typeof errors.value = {}
  const n = name.value.trim()
  if (!n) e.name = 'Give the channel a name.'
  else if (n.length > 60) e.name = 'Keep the name under 60 characters.'
  const p = ui.value.problem(config.value, editing.value)
  if (p) e.config = p
  errors.value = e
  return !e.name && !e.config
}

async function save() {
  if (!check()) return
  busy.value = true
  try {
    let saved: NotificationChannel
    if (props.channel) {
      const cfg = Object.fromEntries(Object.entries(config.value).filter(([, v]) => String(v ?? '').trim() !== ''))
      saved = await api.updateNotificationChannel(props.channel.id, {
        name: name.value.trim(),
        events: events.value,
        config: Object.keys(cfg).length ? cfg : undefined,
      })
    } else {
      let cfg = config.value
      if (type.value === 'browser') cfg = { ...(await subscribe(props.options.vapid_public_key)) }
      saved = await api.createNotificationChannel({ type: type.value, name: name.value.trim(), events: events.value, config: cfg })
    }
    open.value = false
    emit('saved', saved)
  } catch (e) {
    if (e instanceof PushDenied) errors.value = { config: 'Notifications were not allowed. Allow them in the site settings (the icon left of the address bar) and try again.' }
    else if (e instanceof ApiError && /webhook|push_subscription/.test(e.code)) errors.value = { config: e.message }
    else if (e instanceof ApiError && e.code === 'channel_name_invalid') errors.value = { name: e.message }
    else toastError(e)
  } finally {
    busy.value = false
  }
}

const saveLabel = computed(() => {
  if (busy.value) return 'Saving...'
  if (editing.value) return 'Save'
  return type.value === 'browser' ? 'Turn on in this browser' : 'Add channel'
})
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{{ editing ? `Edit ${channel?.name}` : 'Add a notification channel' }}</DialogTitle>
        <DialogDescription>Pick where messages go and which events send one.</DialogDescription>
      </DialogHeader>
      <form id="notification-channel" class="grid gap-5" novalidate @submit.prevent="save">
        <div v-if="!editing && types.length > 1" class="grid gap-2" role="radiogroup" aria-label="Channel type">
          <button
            v-for="t in types"
            :key="t.type"
            type="button"
            role="radio"
            :aria-checked="type === t.type"
            :class="cn('flex items-start gap-3 rounded-lg border p-3 text-left transition-colors hover:bg-muted/50', type === t.type && 'border-primary bg-primary/5')"
            @click="pickType(t.type)"
          >
            <component :is="t.icon" class="mt-0.5 size-4 shrink-0" :class="type === t.type ? 'text-primary-text' : 'text-muted-foreground'" />
            <span class="grid gap-0.5">
              <span class="text-sm font-medium">{{ t.label }}</span>
              <span class="text-xs text-muted-foreground">{{ t.blurb }}</span>
            </span>
          </button>
        </div>

        <div class="grid gap-2">
          <Label for="channel-name">Name</Label>
          <Input id="channel-name" v-model="name" autocomplete="off" :placeholder="type === 'discord' ? '#deploys' : 'Chrome on macOS'" :aria-invalid="errors.name ? true : undefined" />
          <p v-if="errors.name" class="text-sm text-destructive">{{ errors.name }}</p>
        </div>

        <component
          :is="ui.form"
          v-model="config"
          :existing="channel?.config"
          :error="errors.config"
          id-prefix="channel"
          :push-state="pushState"
          :current="current"
        />

        <EventPicker v-model="events" :events="options.events" :type="type" id-prefix="channel-event" />
      </form>
      <DialogFooter>
        <Button variant="outline" @click="open = false">Cancel</Button>
        <Button type="submit" form="notification-channel" :disabled="busy || browserBlocked">{{ saveLabel }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
