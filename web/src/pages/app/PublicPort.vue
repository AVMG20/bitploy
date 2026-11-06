<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { Eye, EyeOff, Globe, TriangleAlert } from 'lucide-vue-next'
import CopyButton from '@/components/CopyButton.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import { isMulti } from '@/lib/services'
import type { App, CloseAfter, PublicConnection, PublicPortInput } from '@/lib/types'

// A port on the server that forwards to a database (or a worker, or a service
// of a compose app), so its owner can connect with psql, TablePlus or
// redis-cli. Off by default, closes by itself after an hour unless asked
// otherwise. compact: in the canvas panel, where an unavailable port is left out.
const props = defineProps<{ app: App; compact?: boolean }>()
const qc = useQueryClient()

const pp = computed(() => props.app.public_port)
const isOpen = computed(() => !!pp.value?.enabled)
// Services that can get the port: the ones with a known port and, for a web
// app with several services, not the one with the address. Ready-made images
// (databases, caches) first: those are what people connect to.
const candidates = computed(() =>
  isMulti(props.app)
    ? props.app.services
        .filter((s) => !(s.public && props.app.type === 'web') && s.port)
        .sort((a, b) => Number(b.kind === 'image') - Number(a.kind === 'image'))
        .map((s) => ({ name: s.name, port: s.port }))
    : [],
)
const description = computed(() =>
  props.app.template
    ? 'Connect to it from your computer with psql, TablePlus or a similar tool. Close the port when you are done.'
    : isMulti(props.app)
      ? 'Reach one of its services from your computer, like the database with psql or TablePlus. Close the port when you are done.'
      : `Connect to its port${props.app.port ? ` ${props.app.port}` : ''} from your computer, for example to test it. Close the port when you are done.`,
)

// ---------------------------------------------------------------- the form (opening, or changing while open)
const formOpen = ref(false)
const closeAfter = ref<CloseAfter>('1h')
const who = ref<'anyone' | 'me' | 'list'>('me')
const allowText = ref('')
const service = ref('')
const CLOSE_CHOICES: { value: CloseAfter; label: string }[] = [
  { value: '1h', label: '1 hour' },
  { value: '24h', label: '24 hours' },
  { value: 'never', label: 'Never' },
]
const WHO_CHOICES = [
  { value: 'me', label: 'Only my IP' },
  { value: 'list', label: 'These addresses' },
  { value: 'anyone', label: 'Anyone' },
] as const

function resetForm() {
  const p = pp.value
  closeAfter.value = isOpen.value && !p?.expires_at ? 'never' : '1h'
  // Opening: only this computer by default, the safest choice. Open: what is set now.
  who.value = p?.allow_from.length ? 'list' : isOpen.value ? 'anyone' : 'me'
  allowText.value = (p?.allow_from ?? []).join(', ')
  service.value = p?.service || candidates.value[0]?.name || ''
}

// What the reveal button showed; gone when the port closes or moves.
const revealed = ref<PublicConnection | null>(null)
let loadedFor = ''
watch(
  () => [props.app.id, pp.value?.enabled, pp.value?.port] as const,
  ([id, on, port]) => {
    if (loadedFor !== `${id}:${on}:${port}`) {
      loadedFor = `${id}:${on}:${port}`
      formOpen.value = false
      revealed.value = null
      resetForm()
    }
  },
  { immediate: true },
)

const refresh = () => {
  void qc.invalidateQueries({ queryKey: keys.app(props.app.slug) })
  void qc.invalidateQueries({ queryKey: keys.apps })
  void qc.invalidateQueries({ queryKey: keys.adminPublicPorts })
}

const parsedAllow = computed(() =>
  allowText.value
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean),
)
const allowError = computed(() => (who.value === 'list' && !parsedAllow.value.length ? 'Add at least one IPv4 address, like 203.0.113.7.' : ''))

const save = useMutation({
  mutationFn: () => {
    const body: PublicPortInput = {
      enabled: true,
      close_after: closeAfter.value,
      allow_from: who.value === 'list' ? parsedAllow.value : [],
      only_my_ip: who.value === 'me',
    }
    if (candidates.value.length && !isOpen.value) body.service = service.value
    return api.setPublicPort(props.app.slug, body)
  },
  onSuccess: (updated) => {
    const wasOpen = isOpen.value
    qc.setQueryData(keys.app(updated.slug), updated)
    formOpen.value = false
    refresh()
    const p = updated.public_port
    toastOk(wasOpen ? 'Public port changed' : 'Public port open', p?.address ? `Connect to ${p.address}.` : undefined)
  },
  // A port that could not start is saved anyway (bitploy keeps trying), so show what is there now.
  onError: (e) => {
    toastError(e)
    refresh()
  },
})

const close = useMutation({
  mutationFn: () => api.setPublicPort(props.app.slug, { enabled: false }),
  onSuccess: (updated) => {
    qc.setQueryData(keys.app(updated.slug), updated)
    refresh()
    toastOk('Public port closed', 'Nobody can connect from outside any more.')
  },
  onError: (e) => toastError(e),
})

const reveal = useMutation({
  mutationFn: () => api.revealPublicConnection(props.app.slug),
  onSuccess: (r) => (revealed.value = r),
  onError: (e) => toastError(e),
})

function toggle(on: boolean) {
  if (on) {
    resetForm()
    formOpen.value = true
  } else if (isOpen.value) {
    close.mutate()
  } else {
    formOpen.value = false
  }
}
function submit() {
  if (allowError.value) return
  save.mutate()
}

const busy = computed(() => save.isPending.value || close.isPending.value)
const secrets = computed(() => Object.entries(revealed.value?.secrets ?? {}))
const shownURL = computed(() => revealed.value?.connection_url || pp.value?.connection_url || '')
const visible = computed(() => !!pp.value && (pp.value.enabled || pp.value.available || (!props.compact && pp.value.reason === 'public_ports_off')))
</script>

<template>
  <Card v-if="visible && pp" data-testid="public-port">
    <CardHeader>
      <CardTitle class="flex items-center gap-2"><Globe class="size-4 text-muted-foreground" /> Public port</CardTitle>
      <CardDescription>{{ description }}</CardDescription>
    </CardHeader>
    <CardContent class="grid gap-4">
      <p v-if="!pp.available && !pp.enabled" class="text-sm text-muted-foreground">
        This server hands out no public ports. Your admin can turn them on.
      </p>
      <p v-else-if="!pp.enabled && isMulti(app) && !candidates.length" class="text-sm text-muted-foreground">
        None of its services has a known port. Add one with expose in the compose file and deploy.
      </p>
      <template v-else>
        <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2" data-testid="public-port-state">
          <div class="min-w-0 text-sm">
            <p class="font-medium">{{ isOpen ? 'Open' : formOpen ? 'Not open yet' : 'Closed' }}</p>
            <p class="text-xs text-muted-foreground">
              <template v-if="isOpen && pp.expires_at">Closes by itself <RelativeTime :date="pp.expires_at" />.</template>
              <template v-else-if="isOpen">Stays open until you close it.</template>
              <template v-else-if="formOpen">Pick who can connect, then press Open port. Until then only the apps of its project can reach it.</template>
              <template v-else>Only the apps of its project can reach it.</template>
            </p>
          </div>
          <Button v-if="!isOpen && formOpen" variant="ghost" size="sm" :disabled="busy" @click="toggle(false)">Cancel</Button>
          <Button v-else-if="!isOpen" variant="outline" size="sm" :disabled="busy" @click="toggle(true)">Open a port</Button>
        </div>

        <!-- Open: where to connect. -->
        <div v-if="isOpen" class="grid gap-3">
          <div class="grid gap-1">
            <Label class="text-xs text-muted-foreground">Address</Label>
            <div class="flex min-w-0 items-center gap-1 rounded-md border bg-muted/30 px-2.5 py-1.5">
              <span class="truncate font-mono text-sm" data-testid="public-port-address">{{ pp.address }}</span>
              <CopyButton v-if="pp.address" :value="pp.address" label="Copy address" what="Address copied" icon class="ml-auto size-7 text-muted-foreground" />
            </div>
            <p class="text-xs">
              <span v-if="pp.error" class="grid gap-0.5 text-destructive" role="alert"><span>{{ pp.error }}</span><span class="text-muted-foreground">Not forwarding yet. bitploy tries again every minute.</span></span>
              <span v-else-if="pp.running" class="inline-flex items-center gap-1.5 text-muted-foreground">
                <span class="size-1.5 rounded-full bg-success" aria-hidden="true" /> Forwarding to {{ pp.service ? `${pp.service}, ` : '' }}port {{ pp.target_port }}
              </span>
              <span v-else class="text-muted-foreground">Starting...</span>
            </p>
          </div>

          <div v-if="shownURL" class="grid gap-1">
            <Label class="text-xs text-muted-foreground">Connection string</Label>
            <div class="flex min-w-0 items-center gap-1 rounded-md border bg-muted/30 px-2.5 py-1.5">
              <span class="min-w-0 flex-1 font-mono text-xs break-all" data-testid="public-port-url">{{ shownURL }}</span>
              <Button
                type="button"
                size="icon-sm"
                variant="ghost"
                class="text-muted-foreground"
                :aria-label="revealed ? 'Hide password' : 'Show password'"
                :aria-pressed="!!revealed"
                :disabled="reveal.isPending.value"
                @click="revealed ? (revealed = null) : reveal.mutate()"
              >
                <EyeOff v-if="revealed" />
                <Eye v-else />
              </Button>
              <CopyButton v-if="revealed" :value="revealed.connection_url" label="Copy connection string" what="Connection string copied" icon class="size-7 text-muted-foreground" />
            </div>
            <p class="text-xs text-muted-foreground">{{ revealed ? 'It holds the password: do not paste it in a chat or a file in your code.' : 'Show the password to copy it.' }}</p>
          </div>
          <ul v-if="secrets.length" class="divide-y rounded-lg border text-xs">
            <li v-for="[name, value] in secrets" :key="name" class="flex min-w-0 items-center gap-2 px-3 py-1.5">
              <span class="shrink-0 font-mono font-medium">{{ name }}</span>
              <span class="ml-auto truncate font-mono text-muted-foreground">{{ value }}</span>
              <CopyButton :value="value" :label="`Copy ${name}`" :what="`${name} copied`" icon class="size-7 text-muted-foreground" />
            </li>
          </ul>

          <p class="text-xs text-muted-foreground">
            {{
              pp.allow_from.length
                ? `Only ${pp.allow_from.join(', ')} can connect.`
                : app.template
                  ? 'Anyone who has the address and the password can connect.'
                  : 'Anyone who has the address can connect. Make sure the service asks for a password.'
            }}
          </p>
          <div v-if="!formOpen" class="flex flex-wrap gap-2">
            <Button size="sm" variant="outline" :disabled="busy" @click="resetForm(); formOpen = true">Change settings</Button>
            <Button size="sm" variant="outline" :disabled="busy" @click="close.mutate()">{{ close.isPending.value ? 'Closing...' : 'Close port' }}</Button>
          </div>
        </div>

        <!-- The choices, before opening or to change them. -->
        <form v-if="formOpen" class="grid gap-4" novalidate @submit.prevent="submit">
          <div v-if="candidates.length > 1 && !isOpen" class="grid gap-1.5">
            <Label :for="`pp-service-${app.id}`">Service</Label>
            <Select v-model="service">
              <SelectTrigger :id="`pp-service-${app.id}`" class="w-full sm:max-w-xs"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="s in candidates" :key="s.name" :value="s.name">{{ s.name }} (port {{ s.port }})</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <fieldset class="grid gap-1.5">
            <legend class="mb-1.5 text-sm font-medium">Close automatically after<span v-if="isOpen" class="font-normal text-muted-foreground"> (counted from now)</span></legend>
            <div role="radiogroup" class="inline-flex w-fit rounded-md border bg-muted/30 p-0.5">
              <button
                v-for="c in CLOSE_CHOICES"
                :key="c.value"
                type="button"
                role="radio"
                :aria-checked="closeAfter === c.value"
                :class="['rounded px-2.5 py-1 text-xs font-medium transition-colors', closeAfter === c.value ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground']"
                @click="closeAfter = c.value"
              >
{{ c.label }}
</button>
            </div>
          </fieldset>

          <fieldset class="grid gap-1.5">
            <legend class="mb-1.5 text-sm font-medium">Who can connect</legend>
            <div role="radiogroup" class="inline-flex w-fit flex-wrap rounded-md border bg-muted/30 p-0.5">
              <button
                v-for="c in WHO_CHOICES"
                :key="c.value"
                type="button"
                role="radio"
                :aria-checked="who === c.value"
                :class="['rounded px-2.5 py-1 text-xs font-medium transition-colors', who === c.value ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground']"
                @click="who = c.value"
              >
{{ c.label }}
</button>
            </div>
            <p v-if="who === 'me'" class="text-xs text-muted-foreground">The IPv4 address you use bitploy from now. It changes when you switch networks.</p>
            <template v-if="who === 'list'">
              <Input v-model="allowText" :aria-invalid="!!allowError" aria-label="Addresses that can connect" placeholder="203.0.113.7, 198.51.100.0/24" autocapitalize="off" autocomplete="off" spellcheck="false" class="font-mono" />
              <p class="text-xs" :class="allowError ? 'text-destructive' : 'text-muted-foreground'">{{ allowError || 'IPv4 addresses or networks, separated by commas.' }}</p>
            </template>
          </fieldset>

          <div v-if="!isOpen" role="note" class="flex gap-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-xs">
            <TriangleAlert class="mt-px size-3.5 shrink-0" aria-hidden="true" />
            <span>The port is on the internet. Anyone who finds it can try passwords, so pick who can connect and close it when you are done.</span>
          </div>

          <div class="flex flex-wrap gap-2">
            <Button type="submit" size="sm" :disabled="busy || !!allowError">
              {{ save.isPending.value ? (isOpen ? 'Saving...' : 'Opening...') : isOpen ? 'Save changes' : 'Open port' }}
            </Button>
            <Button v-if="isOpen" type="button" size="sm" variant="ghost" :disabled="busy" @click="formOpen = false">Cancel</Button>
          </div>
        </form>
      </template>
    </CardContent>
  </Card>
</template>
