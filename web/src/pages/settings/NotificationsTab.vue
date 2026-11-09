<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { Bell, BellRing, Pencil, Plus, Send, Trash2 } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import SettingsSection from '@/components/SettingsSection.vue'
import ChannelDialog from '@/components/notifications/ChannelDialog.vue'
import { CHANNEL_TYPES } from '@/components/notifications/channelTypes'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { browserName, channelHealth, channelTarget, currentBrowserChannel, defaultEvents, pushBlockedText, type PushState } from '@/lib/notifications'
import { currentEndpointHash, PushDenied, pushState as readPushState, subscribe, unsubscribe } from '@/lib/push'
import { toastError, toastOk } from '@/lib/notify'
import { quietDestructive } from '@/lib/utils'
import type { NotificationChannel, NotificationChannelType } from '@/lib/types'

const qc = useQueryClient()
const options = useQuery({ queryKey: keys.notificationOptions, queryFn: api.notificationOptions, staleTime: Infinity })
const channels = useQuery({ queryKey: keys.notificationChannels, queryFn: api.notificationChannels })
const refresh = () => qc.invalidateQueries({ queryKey: keys.notificationChannels })

// ---------------------------------------------------------------- this browser
const pushState = ref<PushState>(readPushState())
const currentHash = ref<string | null>(null)
const browserOffered = computed(() => !!options.data.value?.types.some((t) => t.type === 'browser'))
const thisBrowser = computed(() => currentBrowserChannel(channels.data.value ?? [], currentHash.value))
const blockedText = computed(() => pushBlockedText(pushState.value, browserName(navigator.userAgent)))
async function readBrowser() {
  pushState.value = readPushState()
  currentHash.value = await currentEndpointHash()
}
onMounted(readBrowser)

const turnOn = useMutation({
  mutationFn: async () => {
    const o = options.data.value!
    const sub = await subscribe(o.vapid_public_key)
    return api.createNotificationChannel({ type: 'browser', name: browserName(navigator.userAgent), events: defaultEvents(o.events), config: { ...sub } })
  },
  onSuccess: async (c) => {
    toastOk('Notifications are on in this browser', 'Send a test message to see one.')
    await readBrowser()
    qc.setQueryData<NotificationChannel[]>(keys.notificationChannels, (list) => [...(list ?? []).filter((x) => x.id !== c.id), c])
    void refresh()
  },
  onError: async (e) => {
    await readBrowser()
    if (!(e instanceof PushDenied)) toastError(e)
  },
})

// ---------------------------------------------------------------- channels
const dialogOpen = ref(false)
const editing = ref<NotificationChannel | null>(null)
const startType = ref<NotificationChannelType>('discord')
function add(type: NotificationChannelType = 'discord') {
  editing.value = null
  startType.value = type
  dialogOpen.value = true
}
function edit(c: NotificationChannel) {
  editing.value = c
  dialogOpen.value = true
}
async function onSaved(c: NotificationChannel) {
  toastOk(editing.value ? 'Channel saved' : `${c.name} added`)
  if (c.type === 'browser') await readBrowser()
  void refresh()
}

const toggle = useMutation({
  mutationFn: (v: { c: NotificationChannel; on: boolean }) => api.updateNotificationChannel(v.c.id, { enabled: v.on }),
  onSuccess: () => refresh(),
  onError: (e) => toastError(e),
})

const testing = ref<string | null>(null)
const test = useMutation({
  mutationFn: (c: NotificationChannel) => {
    testing.value = c.id
    return api.testNotificationChannel(c.id)
  },
  onSuccess: (c) => toastOk('Test message sent', `Check ${c.name}.`),
  onError: (e) => toastError(e, 'The test message was not sent.'),
  onSettled: () => {
    testing.value = null
    void refresh()
  },
})

const toDelete = ref<NotificationChannel | null>(null)
const deleteOpen = computed({
  get: () => toDelete.value !== null,
  set: (v) => {
    if (!v) toDelete.value = null
  },
})
const remove = useMutation({
  mutationFn: async (c: NotificationChannel) => {
    await api.deleteNotificationChannel(c.id)
    if (c.id === thisBrowser.value?.id) await unsubscribe()
  },
  onSuccess: async () => {
    toastOk('Channel deleted')
    toDelete.value = null
    await readBrowser()
    void refresh()
  },
  onError: (e) => toastError(e),
})

const typeLabel = (t: NotificationChannelType) => (t === 'browser' ? 'Browser' : (CHANNEL_TYPES[t]?.label ?? t))
const eventCount = (c: NotificationChannel) => (c.events.length === 1 ? '1 event' : `${c.events.length} events`)
</script>

<template>
  <SettingsSection
    title="Notifications"
    description="Messages about deployments, backups and crashes of your apps and of the projects shared with you."
  >
    <template v-if="channels.data.value?.length" #aside>
      <Button size="sm" @click="add()"><Plus /> Add channel</Button>
    </template>

    <QueryError v-if="options.isError.value || channels.isError.value" :error="options.error.value ?? channels.error.value" @retry="options.refetch(); channels.refetch()" />
    <div v-else-if="options.isPending.value || channels.isPending.value" class="grid gap-2"><Skeleton v-for="i in 2" :key="i" class="h-20 w-full" /></div>
    <div v-else class="grid gap-4">
      <!-- This browser -->
      <div v-if="browserOffered" class="flex flex-wrap items-center gap-3 rounded-xl border bg-card p-4 shadow-card" data-testid="this-browser">
        <div class="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-muted/40">
          <BellRing class="size-4" :class="thisBrowser?.enabled ? 'text-primary-text' : 'text-muted-foreground'" />
        </div>
        <div class="grid min-w-0 flex-1 gap-0.5">
          <p class="text-sm font-medium">This browser</p>
          <p v-if="thisBrowser" class="text-sm text-muted-foreground">
            {{ thisBrowser.enabled ? `On, as ${thisBrowser.name}. Logging out turns them off here.` : `${thisBrowser.name} is turned off.` }}
          </p>
          <p v-else-if="blockedText" class="text-sm text-muted-foreground">{{ blockedText }}</p>
          <p v-else class="text-sm text-muted-foreground">Get notifications on this device, also when no bitploy tab is open.</p>
        </div>
        <Button v-if="!thisBrowser && !blockedText" size="sm" variant="outline" :disabled="turnOn.isPending.value" @click="turnOn.mutate()">
          <Bell /> {{ turnOn.isPending.value ? 'Turning on...' : 'Turn on' }}
        </Button>
      </div>

      <EmptyState
        v-if="!channels.data.value?.length"
        :icon="Bell"
        title="No notification channels yet."
        description="Add a Discord webhook to get messages in a Discord channel."
      >
        <Button @click="add('discord')"><Plus /> Add Discord webhook</Button>
      </EmptyState>

      <ul v-else class="grid gap-3" aria-label="Notification channels">
        <li v-for="c in channels.data.value" :key="c.id" class="rounded-xl border bg-card p-4 shadow-card" :data-channel="c.id">
          <div class="flex flex-wrap items-start gap-3">
            <component :is="CHANNEL_TYPES[c.type]?.icon ?? Bell" class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            <div class="grid min-w-0 flex-1 gap-1">
              <div class="flex flex-wrap items-center gap-2">
                <p class="truncate font-medium">{{ c.name }}</p>
                <Badge variant="secondary">{{ typeLabel(c.type) }}</Badge>
                <Badge v-if="thisBrowser?.id === c.id" variant="outline" class="border-primary/40 text-primary-text">This browser</Badge>
              </div>
              <p class="text-sm text-muted-foreground">
                {{ [channelTarget(c), c.events.length ? eventCount(c) : 'No events'].filter(Boolean).join(' · ') }}
              </p>
              <p v-if="channelHealth(c) === 'failing'" class="text-sm text-destructive">
                Last message failed <RelativeTime :date="c.last_error_at" />. {{ c.last_error }}
              </p>
              <p v-else-if="channelHealth(c) === 'ok'" class="text-sm text-muted-foreground">Last message sent <RelativeTime :date="c.last_success_at" /></p>
              <p v-else class="text-sm text-muted-foreground">No messages sent yet</p>
            </div>
            <Switch
              :model-value="c.enabled"
              :aria-label="c.enabled ? `Turn off ${c.name}` : `Turn on ${c.name}`"
              :disabled="toggle.isPending.value"
              @update:model-value="(on: boolean) => toggle.mutate({ c, on })"
            />
          </div>
          <div class="mt-3 flex flex-wrap gap-2 border-t pt-3">
            <Button size="sm" variant="outline" :disabled="testing === c.id" @click="test.mutate(c)">
              <Send /> {{ testing === c.id ? 'Sending...' : 'Send test message' }}
            </Button>
            <Button size="sm" variant="ghost" @click="edit(c)"><Pencil /> Edit</Button>
            <Button size="sm" variant="ghost" :class="quietDestructive" :aria-label="`Delete ${c.name}`" @click="toDelete = c"><Trash2 /> Delete</Button>
          </div>
        </li>
      </ul>
    </div>

    <ChannelDialog
      v-if="options.data.value"
      v-model:open="dialogOpen"
      :options="options.data.value"
      :channel="editing"
      :start-type="startType"
      :push-state="pushState"
      :current-hash="currentHash"
      :browser-added="!!thisBrowser"
      @saved="onSaved"
    />

    <ConfirmDialog
      v-model:open="deleteOpen"
      title="Delete this channel?"
      :description="`${toDelete?.name ?? 'This channel'} stops getting notifications.`"
      confirm-label="Delete channel"
      :loading="remove.isPending.value"
      @confirm="toDelete && remove.mutate(toDelete)"
    />
  </SettingsSection>
</template>
