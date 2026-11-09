<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Archive, Database, Download, HardDrive, Loader2, RotateCcw, Trash2, Upload } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import FormCard from '@/components/FormCard.vue'
import FormScope from '@/components/FormScope.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { keys, useApp, useBackups, useMe, useServiceTemplates } from '@/composables/queries'
import DataFiles from '@/pages/app/DataFiles.vue'
import { ApiError, api, paths } from '@/lib/api'
import { formatBytes, formatDateTime, formatMB, pct } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import { markRestartNeeded } from '@/composables/useRestartNeeded'
import type { Backup, BackupKind, BackupSchedule } from '@/lib/types'
import { useQueryParam } from '@/composables/useQueryParam'
import { isMulti, serviceRole } from '@/lib/services'
import { isServerDb } from '@/lib/dbvalue'

const props = defineProps<{ slug: string }>()
const qc = useQueryClient()
const app = useApp(() => props.slug)
const me = useMe()
const backups = useBackups(() => props.slug)
const quota = computed(() => me.data.value?.quota)
const data = computed(() => app.data.value)

// A project with several services can have several volumes, each mounted by
// one service. Their sizes come from the compose file, so there is no switch
// and no limit here; files and backups are per volume (?volume=).
const multi = computed(() => isMulti(data.value))
const picked = useQueryParam('volume', '')
const volumes = computed(() => data.value?.volumes ?? [])
const vol = computed(() => (multi.value ? (volumes.value.find((v) => v.name === picked.value) ?? volumes.value[0]) : undefined))
const volName = computed(() => vol.value?.name)
const volService = computed(() => data.value?.services.find((s) => s.name === vol.value?.service))
// The database viewer reads the first volume only.
const isFirstVolume = computed(() => !!vol.value && vol.value.name === volumes.value[0]?.name)
const hasData = computed(() => (multi.value ? !!vol.value : !!data.value?.volume))

// A database, cache or search engine made from a ready-made template keeps its
// data in its own folder. A switch that turns that off, or a file browser of
// files only the engine reads, would only let you break it: it gets a view of
// its size and limit, and links to its backups and the Database tab.
const templates = useServiceTemplates()
const service = computed(() => !multi.value && !!data.value?.template && !!data.value?.volume)
const serviceName = computed(() => templates.data.value?.find((t) => t.id === data.value?.template)?.name || data.value?.template || '')
const serviceMount = computed(() => data.value?.volumes?.[0]?.mount_path || '/data')
const serverDb = computed(() => isServerDb(data.value))
function scrollToBackups() {
  document.getElementById('backups')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
// A link to storage#backups (from the Database tab) opens at the backups once they are drawn.
const route = useRoute()
watch(
  () => [route.hash, !!data.value] as const,
  ([hash, ready]) => {
    if (hash === '#backups' && ready) void nextTick(scrollToBackups)
  },
  { immediate: true },
)
const allBackups = computed(() => backups.data.value)
// Backups of the shown volume; one without a volume is of the first.
const list = computed(() => {
  const l = allBackups.value
  if (!l || !multi.value) return l
  const first = volumes.value[0]?.name
  return { ...l, items: l.items.filter((b) => (b.volume || first) === volName.value) }
})

// ---------------------------------------------------------------- /data on or off, and its limit
// The switch applies at once (no Save). The limit has its own Save button.
const dataForm = useForm({
  validationSchema: computed(() =>
    toTypedSchema(
      z.object({
        volume_gb: z.coerce
          .number({ invalid_type_error: 'Enter a number.' })
          .min(0.1, 'At least 0.1 GB.')
          .max((quota.value?.volume_mb || 1024 * 1024) / 1024, `Your plan allows up to ${formatMB(quota.value?.volume_mb)} in total.`),
      }),
    ),
  ),
  initialValues: { volume_gb: 5 },
})

let dataLoadedFor = ''
watch(
  () => app.data.value,
  (a) => {
    // Fill the form once per app (not on every poll, which would wipe unsaved edits).
    if (a && dataLoadedFor !== a.id) {
      dataLoadedFor = a.id
      dataForm.resetForm({ values: { volume_gb: a.volume_mb / 1024 } })
    }
  },
  { immediate: true },
)

function refreshApp() {
  void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
  void qc.invalidateQueries({ queryKey: keys.backups(props.slug) })
  void qc.invalidateQueries({ queryKey: keys.me })
}

const saveLimit = useMutation({
  mutationFn: (v: { volume_gb: number }) => api.updateApp(props.slug, { volume_mb: Math.round(v.volume_gb * 1024) }),
  onSuccess: (updated) => {
    toastOk('Saved')
    dataForm.resetForm({ values: { volume_gb: updated.volume_mb / 1024 } })
    refreshApp()
  },
  onError: (e) => toastError(e),
})
const submitLimit = dataForm.handleSubmit((v) => saveLimit.mutate(v))

const turnOn = useMutation({
  mutationFn: () => api.updateApp(props.slug, { volume: true }),
  onSuccess: (updated) => {
    toastOk('Saved files are on', data.value?.status === 'running' ? 'Restart the app to start using them.' : 'The app uses them the next time it starts.')
    markRestartNeeded(data.value, 'storage')
    dataForm.resetForm({ values: { volume_gb: updated.volume_mb / 1024 } })
    refreshApp()
  },
  onError: (e) => toastError(e),
})

// Turning /data off removes the Docker volume and all backups. It goes ahead
// at once when there is nothing to lose; otherwise the user types the app name.
// The server is the judge: it refuses with volume_has_data when it finds files
// (the "in use" figure is only measured hourly), and then the dialog opens.
const confirmOffOpen = ref(false)
const turnOff = useMutation({
  mutationFn: (deleteData: boolean) => api.updateApp(props.slug, { volume: false, delete_data: deleteData }),
  onSuccess: () => {
    confirmOffOpen.value = false
    toastOk('Saved files are off')
    refreshApp()
  },
  onError: (e) => {
    if (e instanceof ApiError && e.code === 'volume_has_data' && !confirmOffOpen.value) {
      confirmOffOpen.value = true
      refreshApp()
      return
    }
    confirmOffOpen.value = false
    toastError(e)
  },
})

const switching = computed(() => turnOn.isPending.value || turnOff.isPending.value)
const isStatic = computed(() => !!data.value?.static)
const backupCount = computed(() => list.value?.items.length ?? 0)

function onSwitch(on: boolean) {
  if (on) {
    turnOn.mutate()
  } else if (backupCount.value > 0 || (data.value?.volume_used_mb ?? 0) > 0) {
    confirmOffOpen.value = true
  } else {
    turnOff.mutate(false)
  }
}

const confirmOffText = computed(() => {
  const n = backupCount.value
  const backups = n === 0 ? '' : ` and all ${n} ${n === 1 ? 'backup' : 'backups'}`
  return `This deletes all saved files${backups}. This cannot be undone.`
})

const storageFree = computed(() => {
  const m = me.data.value
  if (!m) return ''
  const taken = m.usage.files_mb + m.usage.backups_mb + m.usage.volume_reserved_mb
  return `${formatMB(Math.max(0, m.quota.volume_mb - taken))} of your ${formatMB(m.quota.volume_mb)} storage is free.`
})

// ---------------------------------------------------------------- automatic backups
const backupForm = useForm({
  validationSchema: toTypedSchema(
    z
      .object({
        schedule: z.enum(['off', 'every_6h', 'every_12h', 'daily', 'weekly', 'monthly']),
        keep_mode: z.enum(['last', 'days']),
        keep: z.coerce.number({ invalid_type_error: 'Enter a number.' }).int('Use a whole number.').min(1, 'At least 1.'),
      })
      .superRefine((v, ctx) => {
        const max = v.keep_mode === 'last' ? 100 : 365
        if (v.keep > max) ctx.addIssue({ code: 'custom', path: ['keep'], message: `At most ${max}.` })
      }),
  ),
  initialValues: { schedule: 'daily' as const, keep_mode: 'days' as const, keep: 14 },
})

let settingsLoadedFor = ''
watch(
  () => list.value?.settings,
  (s) => {
    if (s && settingsLoadedFor !== props.slug) {
      settingsLoadedFor = props.slug
      backupForm.resetForm({ values: { schedule: s.schedule, keep_mode: s.keep_mode, keep: s.keep } })
    }
  },
  { immediate: true },
)

const saveSettings = useMutation({
  mutationFn: (v: { schedule: BackupSchedule; keep_mode: 'last' | 'days'; keep: number }) => api.updateBackupSettings(props.slug, v),
  onSuccess: (s) => {
    toastOk('Saved')
    backupForm.resetForm({ values: { schedule: s.schedule, keep_mode: s.keep_mode, keep: s.keep } })
    void qc.invalidateQueries({ queryKey: keys.backups(props.slug) })
  },
  onError: (e) => toastError(e),
})
const submitSettings = backupForm.handleSubmit((v) => saveSettings.mutate(v))

// Each app has its own backup time between 02:00 and 06:00 UTC. Backup times
// in the list are in the browser's time, so the schedule is too, with the zone named.
const backupTime = computed(() => list.value?.settings.time_utc ?? '03:00')
function scheduleAt(schedule: BackupSchedule): string {
  const [h, m] = backupTime.value.split(':').map(Number)
  const d = new Date()
  d.setUTCHours(h ?? 3, m ?? 0, 0, 0)
  const fmt = (x: Date, zone: boolean) =>
    x.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', ...(zone ? { timeZoneName: 'short' as const } : {}) })
  switch (schedule) {
    case 'every_6h':
    case 'every_12h': {
      const step = schedule === 'every_6h' ? 6 : 12
      const times = Array.from({ length: 24 / step }, (_, i) => new Date(d.getTime() + i * step * 3600_000))
      times.sort((a, b) => (a.getHours() * 60 + a.getMinutes()) - (b.getHours() * 60 + b.getMinutes()))
      const labels = times.map((x) => fmt(x, false))
      const last = labels.pop()
      const zone = new Intl.DateTimeFormat(undefined, { timeZoneName: 'short' }).formatToParts(d).find((p) => p.type === 'timeZoneName')?.value
      return `Every ${step} hours, at ${labels.join(', ')} and ${last}${zone ? ` ${zone}` : ''}.`
    }
    case 'weekly': {
      const sunday = new Date(d)
      sunday.setUTCDate(sunday.getUTCDate() - sunday.getUTCDay()) // a Sunday
      return `Every ${sunday.toLocaleDateString(undefined, { weekday: 'long' })} at ${fmt(sunday, true)}.`
    }
    case 'monthly': {
      // The 1st at this time in UTC can fall on the last day of the month before in the user's zone.
      const first = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1, d.getUTCHours(), d.getUTCMinutes()))
      return first.getDate() === 1
        ? `On the 1st of every month at ${fmt(first, true)}.`
        : `On the last day of every month at ${fmt(first, true)}.`
    }
    default:
      return `Every night at ${fmt(d, true)}.`
  }
}
const scheduleHint = computed(() => {
  const s = backupForm.values.schedule
  return s && s !== 'off' ? scheduleAt(s) : 'Only when you press Back up now.'
})
const nextAt = computed(() => {
  const s = list.value?.settings
  return s && s.schedule !== 'off' && s.schedule === backupForm.values.schedule ? s.next_at : undefined
})
const nextIsPast = computed(() => !!nextAt.value && new Date(nextAt.value).getTime() <= Date.now())

// ---------------------------------------------------------------- backups
// A cache, search index or test inbox (valkey, redis, meilisearch, typesense, mailpit) has no backups.
const noBackupsText = computed(() => {
  const t = data.value?.template
  if (data.value?.backups_supported !== false) return ''
  if (t === 'meilisearch' || t === 'typesense') return `${t === 'typesense' ? 'Typesense' : 'Meilisearch'} keeps a search index; it can be rebuilt from your data, so bitploy makes no backups of it.`
  if (t === 'mailpit') return 'Mailpit keeps test email; it is not worth keeping, so bitploy makes no backups of it.'
  return `${t === 'redis' ? 'Redis' : 'Valkey'} keeps a cache; its data can be rebuilt, so bitploy makes no backups of it.`
})
const restoring = computed(() => list.value?.restore?.state === 'running')
const running = computed(() => !!list.value?.busy || createBackup.isPending.value)
const working = computed(() => running.value || restoring.value || uploading.value)
const backingUp = computed(() => !!list.value?.items.some((b) => b.status === 'running' && b.kind !== 'before_restore'))

function refreshBackups() {
  void qc.invalidateQueries({ queryKey: keys.backups(props.slug) })
  void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
  void qc.invalidateQueries({ queryKey: keys.me })
}

const createBackup = useMutation({
  mutationFn: () => api.createBackup(props.slug, volName.value),
  onSuccess: refreshBackups,
  onError: (e) => toastError(e),
})

const fileInput = ref<HTMLInputElement | null>(null)
const progress = ref(0)
const uploading = ref(false)
const processing = ref(false) // everything is sent, the server is checking the archive
async function onFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // so the same file can be picked again
  if (!file) return
  // The server says no to a file that is too big as well, but only after it has
  // arrived: say so before sending it.
  const max = list.value?.upload_max_bytes
  if (max && file.size > max) {
    toastError(new Error(`That file is too big. Backups can be up to ${formatBytes(max)} here.`))
    return
  }
  uploading.value = true
  processing.value = false
  progress.value = 0
  try {
    await api.uploadBackup(
      props.slug,
      file,
      (f) => (progress.value = f),
      () => (processing.value = true),
      volName.value,
    )
    toastOk('Backup uploaded')
    refreshBackups()
  } catch (err) {
    toastError(err)
  } finally {
    uploading.value = false
    processing.value = false
  }
}

const toRestore = ref<Backup | null>(null)
const restoreOpen = computed({
  get: () => toRestore.value !== null,
  set: (v) => {
    if (!v) toRestore.value = null
  },
})
const restore = useMutation({
  mutationFn: (id: string) => api.restoreBackup(props.slug, id),
  onSuccess: () => {
    toRestore.value = null
    refreshBackups()
  },
  onError: (e) => {
    toRestore.value = null
    toastError(e)
  },
})

const toDelete = ref<Backup | null>(null)
const deleteOpen = computed({
  get: () => toDelete.value !== null,
  set: (v) => {
    if (!v) toDelete.value = null
  },
})
const remove = useMutation({
  mutationFn: (id: string) => api.deleteBackup(props.slug, id),
  onSuccess: () => {
    toastOk('Backup deleted')
    toDelete.value = null
    refreshBackups()
  },
  onError: (e) => {
    toDelete.value = null
    toastError(e)
  },
})

// Whether a restore now would stop the app and start it again.
const appWillRestart = computed(() => !!data.value && data.value.desired_state === 'running' && !!data.value.current_deployment)

// Tell the result of a restore once, when it finishes while this page is open.
watch(
  () => list.value?.restore,
  (now, before) => {
    if (before?.state !== 'running' || !now || now.state === 'running') return
    if (now.state === 'done') toastOk('Backup restored', now.app_restarts ? 'The app is running again.' : undefined)
    void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.me })
  },
)
const failedRestore = computed(() => {
  const r = list.value?.restore
  return r && r.state === 'failed' ? r : null
})
// The result stays on the server until it is dismissed, so it does not come back on reload.
const dismissRestore = useMutation({
  mutationFn: () => api.dismissBackupRestore(props.slug),
  onSuccess: refreshBackups,
  onError: (e) => toastError(e),
})

const kindLabel: Record<BackupKind, string> = {
  manual: 'Manual',
  scheduled: 'Automatic',
  uploaded: 'Uploaded',
  before_restore: 'Before restore',
}

// ---------------------------------------------------------------- database dumps
// A PostgreSQL, MySQL or MariaDB service is backed up as a dump made by its own
// tool while it runs. A dump is replayed into the running database: the app is
// not stopped, and a stopped database cannot take one.
const dumpTool = computed(() => list.value?.dump_tool)
const dbRunning = computed(() => data.value?.status === 'running')
const restoringDump = computed(() => {
  const id = list.value?.restore?.backup_id
  return !!id && list.value?.items.find((b) => b.id === id)?.format === 'dump'
})
function restoreTitle(b: Backup) {
  if (!hasData.value) return 'Turn on saved files to restore'
  if (b.format === 'dump' && !dbRunning.value) return 'Start the database to restore a dump'
  return undefined
}
const restoreText = computed(() => {
  if (toRestore.value?.format === 'dump') {
    return 'Every database is dropped and rebuilt from this dump. Apps connected to it lose their connection for a moment. The current databases are dumped first, so you can undo this.'
  }
  return `The saved files are replaced by this backup.${appWillRestart.value ? ' The app restarts.' : ''} The current files are backed up first, so you can undo this.`
})
</script>

<template>
  <div v-if="data" class="grid gap-4">
    <Card v-if="multi">
      <CardHeader class="flex flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3">
        <div class="grid min-w-0 gap-1.5">
          <CardTitle>Saved files</CardTitle>
          <CardDescription>
            Files the services write to a volume, like a database or uploads, are kept between deploys.
            Volumes and their sizes come from your compose file.
          </CardDescription>
        </div>
        <Select v-if="volumes.length > 1" :model-value="vol?.name" @update:model-value="(v) => (picked = String(v) === volumes[0]?.name ? '' : String(v))">
          <SelectTrigger class="w-56" aria-label="Volume"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="v in volumes" :key="v.name" :value="v.name">
              <span class="font-mono">{{ v.name }}</span> <span class="text-xs text-muted-foreground">of {{ v.service }}</span>
            </SelectItem>
          </SelectContent>
        </Select>
      </CardHeader>
      <CardContent>
        <EmptyState v-if="!vol" :icon="HardDrive" title="This app has no saved files." description="No service has a volume, so what they write is lost on every restart and deploy. To keep files, add a volume in your compose file." />
        <div v-else class="grid gap-2">
          <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 text-sm">
            <span class="text-muted-foreground">
              <span class="font-mono text-foreground">{{ vol.name }}</span>, used by
              {{ volService ? `the ${serviceRole(volService).label.toLowerCase()} ` : '' }}<span class="font-mono text-foreground">{{ vol.service }}</span>
              at <span class="font-mono text-foreground">{{ vol.mount_path }}</span>
            </span>
            <span class="shrink-0 font-semibold tabular-nums">
              <template v-if="vol.used_mb !== null">{{ formatMB(vol.used_mb) }} <span class="font-normal text-muted-foreground">of {{ formatMB(vol.size_mb) }}</span></template>
              <template v-else><span class="font-normal text-muted-foreground">Not measured yet, limit {{ formatMB(vol.size_mb) }}</span></template>
            </span>
          </div>
          <Progress class="[&>[data-slot=progress-indicator]]:bg-success" :model-value="pct(vol.used_mb ?? 0, vol.size_mb)" />
        </div>
      </CardContent>
    </Card>

    <FormScope v-else-if="service" :form="dataForm">
      <form novalidate data-testid="service-storage" @submit.prevent="submitLimit">
        <FormCard title="Saved data">
          <template #description>
            {{ serviceName }} keeps its data in <span class="font-mono">{{ serviceMount }}</span>.
          </template>
          <div class="grid gap-4">
            <div class="grid gap-2">
              <div class="flex items-baseline justify-between gap-4 text-sm">
                <span class="text-muted-foreground">In use</span>
                <span class="shrink-0 font-semibold tabular-nums">
                  <template v-if="data.volume_used_mb !== null">{{ formatMB(data.volume_used_mb) }} <span class="font-normal text-muted-foreground">of {{ formatMB(data.volume_mb) }}</span></template>
                  <template v-else><span class="font-normal text-muted-foreground">Not measured yet, limit {{ formatMB(data.volume_mb) }}</span></template>
                </span>
              </div>
              <Progress class="[&>[data-slot=progress-indicator]]:bg-success" :model-value="pct(data.volume_used_mb ?? 0, data.volume_mb)" />
            </div>
            <FormField v-slot="{ componentField }" name="volume_gb">
              <FormItem class="sm:max-w-[calc(50%-0.5rem)]">
                <FormLabel>Limit (GB)</FormLabel>
                <FormControl><Input type="number" min="0.1" step="0.5" inputmode="decimal" v-bind="componentField" /></FormControl>
                <FormMessage />
              </FormItem>
            </FormField>
            <div class="flex flex-wrap gap-2">
              <Button v-if="serverDb" as-child variant="outline" size="sm">
                <RouterLink :to="{ name: 'app-database-default', params: { slug } }"><Database /> Open the Database tab</RouterLink>
              </Button>
              <Button v-if="!noBackupsText" type="button" variant="outline" size="sm" @click="scrollToBackups"><Archive /> Backups</Button>
            </div>
          </div>
          <template #hint>{{ storageFree }}</template>
          <template #actions>
            <Button type="submit" :disabled="saveLimit.isPending.value || !dataForm.meta.value.dirty">{{ saveLimit.isPending.value ? 'Saving...' : 'Save limit' }}</Button>
          </template>
        </FormCard>
      </form>
    </FormScope>

    <FormScope v-else :form="dataForm">
      <form novalidate @submit.prevent="submitLimit">
        <FormCard title="Saved files">
          <template #description>Files your app saves in <span class="font-mono">/data</span>, like uploads or a database, are kept between deploys.</template>
          <div class="grid gap-4">
            <div v-if="data.volume" class="grid gap-2">
              <div class="flex items-baseline justify-between gap-4 text-sm">
                <span class="text-muted-foreground">In use</span>
                <span class="shrink-0 font-semibold tabular-nums">
                  <template v-if="data.volume_used_mb !== null">{{ formatMB(data.volume_used_mb) }} <span class="font-normal text-muted-foreground">of {{ formatMB(data.volume_mb) }}</span></template>
                  <template v-else><span class="font-normal text-muted-foreground">Not measured yet, limit {{ formatMB(data.volume_mb) }}</span></template>
                </span>
              </div>
              <Progress class="[&>[data-slot=progress-indicator]]:bg-success" :model-value="pct(data.volume_used_mb ?? 0, data.volume_mb)" />
            </div>

            <div class="flex flex-row items-center justify-between gap-4 rounded-lg border p-3">
              <div class="grid gap-1">
                <Label for="keep-data">Keep saved files <span class="font-mono text-xs font-normal text-muted-foreground">/data</span></Label>
                <p v-if="isStatic && !data.volume" class="text-[0.8rem] text-muted-foreground">Static sites have no saved files.</p>
                <p v-else class="text-[0.8rem] text-muted-foreground">While this is on, each deploy takes the app offline for a few seconds.</p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <Loader2 v-if="switching" class="size-4 animate-spin text-muted-foreground" />
                <Switch
                  id="keep-data"
                  :model-value="data.volume"
                  :disabled="switching || (isStatic && !data.volume)"
                  @update:model-value="(v) => onSwitch(v === true)"
                />
              </div>
            </div>
            <template v-if="data.volume">
              <FormField v-slot="{ componentField }" name="volume_gb">
                <FormItem class="sm:max-w-[calc(50%-0.5rem)]">
                  <FormLabel>Limit (GB)</FormLabel>
                  <FormControl><Input type="number" min="0.1" step="0.5" inputmode="decimal" v-bind="componentField" /></FormControl>
                  <FormMessage />
                </FormItem>
              </FormField>
            </template>
          </div>
          <template v-if="data.volume" #hint>{{ storageFree }}</template>
          <template v-if="data.volume" #actions>
            <Button type="submit" :disabled="saveLimit.isPending.value || !dataForm.meta.value.dirty">{{ saveLimit.isPending.value ? 'Saving...' : 'Save limit' }}</Button>
          </template>
        </FormCard>
      </form>
    </FormScope>

    <DataFiles v-if="multi && vol" :key="vol.name" :slug="slug" :running="data.status === 'running'" :volume="vol.name" :mount-path="vol.mount_path" :can-open-database="isFirstVolume" />
    <DataFiles v-else-if="!multi && data.volume && !service" :slug="slug" :running="data.status === 'running'" />

    <p v-if="noBackupsText" class="px-1 text-sm text-muted-foreground" data-testid="no-backups">{{ noBackupsText }}</p>
    <Card v-else id="backups" class="scroll-mt-4">
      <CardHeader class="flex flex-row flex-wrap items-center justify-between gap-x-4 gap-y-3">
        <div class="grid min-w-0 gap-1">
          <CardTitle>Backups</CardTitle>
          <CardDescription v-if="hasData && list?.items.length">
            {{ list.items.length }} {{ list.items.length === 1 ? 'backup' : 'backups' }}, {{ formatBytes(list.total_bytes) }} in total.
          </CardDescription>
          <CardDescription v-else-if="dumpTool">Dumps of every database that you can download or restore.</CardDescription>
          <CardDescription v-else>Copies of {{ multi && vol ? `the volume ${vol.name}` : 'the saved files' }} that you can download or restore.</CardDescription>
        </div>
        <div v-if="hasData" class="flex flex-wrap gap-2">
          <input ref="fileInput" type="file" accept=".gz,.tgz,application/gzip,application/x-gzip" class="hidden" @change="onFile" />
          <Button variant="outline" :disabled="working" title="A .tar.gz file with the files for /data" @click="fileInput?.click()">
            <Loader2 v-if="uploading" class="animate-spin" /><Upload v-else />
            {{ uploading ? (processing ? 'Checking file...' : `Uploading ${Math.round(progress * 100)}%`) : 'Upload backup' }}
          </Button>
          <Button :disabled="working" @click="createBackup.mutate()">
            <Loader2 v-if="createBackup.isPending.value || backingUp" class="animate-spin" /><Archive v-else />
            {{ createBackup.isPending.value || backingUp ? 'Backing up...' : 'Back up now' }}
          </Button>
        </div>
      </CardHeader>
      <CardContent class="grid gap-4">
        <Alert v-if="restoring">
          <Loader2 class="animate-spin" />
          <AlertTitle>Restoring a backup...</AlertTitle>
          <AlertDescription>{{ !restoringDump && list?.restore?.app_restarts ? 'The app restarts when it is done. ' : '' }}This can take a few minutes.</AlertDescription>
        </Alert>
        <Alert v-else-if="failedRestore" variant="destructive">
          <AlertTitle>The backup was not restored.</AlertTitle>
          <AlertDescription>
            <p>{{ failedRestore.error }}</p>
            <button type="button" class="w-fit underline" :disabled="dismissRestore.isPending.value" @click="dismissRestore.mutate()">Dismiss</button>
          </AlertDescription>
        </Alert>

        <FormScope v-if="hasData && list && (!multi || isFirstVolume)" :form="backupForm">
          <form class="grid gap-4 sm:grid-cols-2 sm:items-start" novalidate @submit.prevent="submitSettings">
            <FormField v-slot="{ componentField }" name="schedule">
              <FormItem>
                <FormLabel>Automatic backups</FormLabel>
                <Select v-bind="componentField">
                  <FormControl><SelectTrigger class="w-full"><SelectValue /></SelectTrigger></FormControl>
                  <SelectContent>
                    <SelectItem value="off">Off</SelectItem>
                    <SelectItem value="every_6h">Every 6 hours</SelectItem>
                    <SelectItem value="every_12h">Every 12 hours</SelectItem>
                    <SelectItem value="daily">Every night</SelectItem>
                    <SelectItem value="weekly">Every week</SelectItem>
                    <SelectItem value="monthly">Every month</SelectItem>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {{ scheduleHint }}
                  <template v-if="nextAt"> Next: <template v-if="nextIsPast">soon</template><RelativeTime v-else :date="nextAt" />.</template>
                </FormDescription>
                <FormMessage />
              </FormItem>
            </FormField>
            <div class="grid gap-2">
              <Label as="span">Keep</Label>
              <div class="flex items-center gap-2">
                <FormField v-slot="{ componentField }" name="keep">
                  <FormItem class="w-20 shrink-0">
                    <FormControl><Input type="number" min="1" step="1" inputmode="numeric" aria-label="How many to keep" v-bind="componentField" /></FormControl>
                  </FormItem>
                </FormField>
                <FormField v-slot="{ componentField }" name="keep_mode">
                  <FormItem class="min-w-0 flex-1">
                    <Select v-bind="componentField">
                      <FormControl><SelectTrigger class="w-full" aria-label="Count backups or days"><SelectValue /></SelectTrigger></FormControl>
                      <SelectContent>
                        <SelectItem value="last">latest backups</SelectItem>
                        <SelectItem value="days">days of backups</SelectItem>
                      </SelectContent>
                    </Select>
                  </FormItem>
                </FormField>
              </div>
              <p v-if="backupForm.errors.value.keep" class="text-[0.8rem] font-medium text-destructive">{{ backupForm.errors.value.keep }}</p>
            </div>
            <div class="flex justify-end sm:col-span-2">
              <Button type="submit" :disabled="saveSettings.isPending.value || !backupForm.meta.value.dirty">
                {{ saveSettings.isPending.value ? 'Saving...' : 'Save schedule' }}
              </Button>
            </div>
          </form>
        </FormScope>

        <QueryError v-if="backups.isError.value && !list" :error="backups.error.value" @retry="backups.refetch()" />
        <Skeleton v-else-if="!list" class="h-24 w-full" />
        <EmptyState
          v-else-if="!list.items.length && !hasData && !multi"
          :icon="HardDrive"
          title="Saved files are off."
          :description="isStatic ? `Static sites have no saved files.` : `Turn them on to keep your app's files between deploys and back them up.`"
        >
          <Button v-if="!isStatic" :disabled="switching" @click="turnOn.mutate()">{{ turnOn.isPending.value ? 'Turning on...' : 'Turn on saved files' }}</Button>
        </EmptyState>
        <EmptyState
          v-else-if="!list.items.length && hasData"
          :icon="Archive"
          title="No backups yet."
          :description="multi && !isFirstVolume ? 'Back up now. Automatic backups cover every volume.' : 'Back up now, or let the automatic backups above make one.'"
        />
        <p v-else-if="!list.items.length" class="text-sm text-muted-foreground">Nothing to back up: this app has no saved files.</p>
        <ul v-else class="divide-y rounded-lg border">
          <li v-for="b in list.items" :key="b.id" class="flex flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2.5">
            <div class="flex min-w-0 flex-[1_1_16rem] flex-wrap items-center gap-x-3 gap-y-1">
              <span class="text-sm font-medium"><RelativeTime :date="b.created_at" /></span>
              <span class="hidden text-sm text-muted-foreground sm:inline">{{ formatDateTime(b.created_at) }}</span>
              <span v-if="multi && b.volume" class="font-mono text-xs text-muted-foreground">{{ b.volume }}</span>
              <Badge variant="secondary">{{ kindLabel[b.kind] }}</Badge>
              <Badge v-if="b.format === 'dump'" variant="outline" :title="b.tool || undefined">Dump</Badge>
              <Badge v-else-if="dumpTool" variant="outline">Files</Badge>
              <span v-if="b.status === 'running'" class="inline-flex items-center gap-1.5 text-sm text-muted-foreground">
                <Loader2 class="size-3.5 animate-spin" /> Working...
              </span>
              <span v-else-if="b.status === 'failed'" class="min-w-0 text-sm text-destructive">{{ b.error || 'This backup failed.' }}</span>
              <span v-else class="text-sm whitespace-nowrap text-muted-foreground tabular-nums">{{ formatBytes(b.size_bytes) }}</span>
            </div>
            <div class="ml-auto flex gap-1">
              <template v-if="b.status === 'ready'">
                <Button as-child size="sm" variant="ghost">
                  <a :href="paths.appBackupDownload(slug, b.id)" download :aria-label="`Download the backup from ${formatDateTime(b.created_at)}`"><Download /> Download</a>
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  :disabled="working || !!restoreTitle(b)"
                  :title="restoreTitle(b)"
                  :aria-label="`Restore the backup from ${formatDateTime(b.created_at)}`"
                  @click="toRestore = b"
                >
                  <RotateCcw /> Restore
                </Button>
              </template>
              <Button
                v-if="b.status !== 'running'"
                size="icon-sm"
                variant="ghost"
                class="text-muted-foreground hover:text-destructive"
                :disabled="working"
                title="Delete"
                :aria-label="`Delete the backup from ${formatDateTime(b.created_at)}`"
                @click="toDelete = b"
              >
                <Trash2 />
              </Button>
            </div>
          </li>
        </ul>
      </CardContent>
    </Card>

    <ConfirmDialog
      v-model:open="restoreOpen"
      :title="`Restore the backup from ${toRestore ? formatDateTime(toRestore.created_at) : ''}?`"
      :description="restoreText"
      confirm-label="Restore"
      :destructive="false"
      :loading="restore.isPending.value"
      @confirm="toRestore && restore.mutate(toRestore.id)"
    />
    <ConfirmDialog
      v-model:open="confirmOffOpen"
      title="Turn off saved files?"
      :description="confirmOffText"
      confirm-label="Delete and turn off"
      :type-to-confirm="slug"
      :loading="turnOff.isPending.value"
      @confirm="turnOff.mutate(true)"
    />
    <ConfirmDialog
      v-model:open="deleteOpen"
      :title="`Delete the backup from ${toDelete ? formatDateTime(toDelete.created_at) : ''}?`"
      :description="toDelete ? `${kindLabel[toDelete.kind]} backup, ${formatBytes(toDelete.size_bytes)}. This cannot be undone.` : 'This cannot be undone.'"
      confirm-label="Delete"
      :loading="remove.isPending.value"
      @confirm="toDelete && remove.mutate(toDelete.id)"
    />
  </div>
  <div v-else class="grid gap-4">
    <Skeleton class="h-48 w-full" />
    <Skeleton class="h-64 w-full" />
  </div>
</template>
