<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Archive, Download, HardDrive, Loader2, RotateCcw, Trash2, Upload } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
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
import { keys, useApp, useBackups, useMe } from '@/composables/queries'
import { api, paths } from '@/lib/api'
import { formatBytes, formatDateTime, formatMB, pct } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import type { Backup, BackupKind } from '@/lib/types'

const props = defineProps<{ slug: string }>()
const qc = useQueryClient()
const app = useApp(() => props.slug)
const me = useMe()
const backups = useBackups(() => props.slug)
const quota = computed(() => me.data.value?.quota)
const data = computed(() => app.data.value)
const list = computed(() => backups.data.value)

// ---------------------------------------------------------------- /data on or off, and its limit
const dataForm = useForm({
  validationSchema: computed(() =>
    toTypedSchema(
      z.object({
        volume: z.boolean(),
        volume_gb: z.coerce
          .number({ invalid_type_error: 'Enter a number.' })
          .min(0.1, 'At least 0.1 GB.')
          .max((quota.value?.volume_mb || 1024 * 1024) / 1024, `Your plan allows up to ${formatMB(quota.value?.volume_mb)} in total.`),
      }),
    ),
  ),
  initialValues: { volume: false, volume_gb: 5 },
})

let dataLoadedFor = ''
watch(
  () => app.data.value,
  (a) => {
    // Fill the form once per app (not on every poll, which would wipe unsaved edits).
    if (a && dataLoadedFor !== a.id) {
      dataLoadedFor = a.id
      dataForm.resetForm({ values: { volume: a.volume, volume_gb: a.volume_mb / 1024 } })
    }
  },
  { immediate: true },
)

const saveData = useMutation({
  mutationFn: (v: { volume: boolean; volume_gb: number }) =>
    api.updateApp(props.slug, { volume: v.volume, volume_mb: Math.round(v.volume_gb * 1024) }),
  onSuccess: (updated) => {
    if (updated.volume) toastOk('Saved', 'Restart or redeploy the app to start using /data.')
    else toastOk('/data is off', 'Your backups are kept.')
    dataForm.resetForm({ values: { volume: updated.volume, volume_gb: updated.volume_mb / 1024 } })
    void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.me })
  },
  onError: (e) => toastError(e),
})
const submitData = dataForm.handleSubmit((v) => saveData.mutate(v))

const turnOn = useMutation({
  mutationFn: () => api.updateApp(props.slug, { volume: true }),
  onSuccess: (updated) => {
    toastOk('/data is on', 'Restart or redeploy the app to start using it.')
    dataForm.resetForm({ values: { volume: updated.volume, volume_gb: updated.volume_mb / 1024 } })
    void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.me })
  },
  onError: (e) => toastError(e),
})

const storageFree = computed(() => {
  const m = me.data.value
  if (!m) return ''
  const taken = m.usage.files_mb + m.usage.backups_mb + m.usage.volume_reserved_mb
  return `${formatMB(Math.max(0, m.quota.volume_mb - taken))} of your ${formatMB(m.quota.volume_mb)} storage is still free.`
})

// ---------------------------------------------------------------- automatic backups
const backupForm = useForm({
  validationSchema: toTypedSchema(
    z
      .object({
        schedule: z.enum(['off', 'daily', 'weekly']),
        keep_mode: z.enum(['last', 'days']),
        keep: z.coerce.number({ invalid_type_error: 'Enter a number.' }).int('Use a whole number.').min(1, 'At least 1.'),
      })
      .superRefine((v, ctx) => {
        const max = v.keep_mode === 'last' ? 100 : 365
        if (v.keep > max) ctx.addIssue({ code: 'custom', path: ['keep'], message: `At most ${max}.` })
      }),
  ),
  initialValues: { schedule: 'off' as const, keep_mode: 'last' as const, keep: 7 },
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
  mutationFn: (v: { schedule: 'off' | 'daily' | 'weekly'; keep_mode: 'last' | 'days'; keep: number }) => api.updateBackupSettings(props.slug, v),
  onSuccess: (s) => {
    toastOk('Saved')
    backupForm.resetForm({ values: { schedule: s.schedule, keep_mode: s.keep_mode, keep: s.keep } })
    void qc.invalidateQueries({ queryKey: keys.backups(props.slug) })
  },
  onError: (e) => toastError(e),
})
const submitSettings = backupForm.handleSubmit((v) => saveSettings.mutate(v))

const scheduleHint = computed(() => {
  switch (backupForm.values.schedule) {
    case 'daily':
      return 'Every night at 03:00 UTC.'
    case 'weekly':
      return 'Every Sunday at 03:00 UTC.'
    default:
      return 'Only when you back up by hand.'
  }
})
const nextAt = computed(() => {
  const s = list.value?.settings
  return s && s.schedule !== 'off' && s.schedule === backupForm.values.schedule ? s.next_at : undefined
})
const nextIsPast = computed(() => !!nextAt.value && new Date(nextAt.value).getTime() <= Date.now())

// ---------------------------------------------------------------- backups
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
  mutationFn: () => api.createBackup(props.slug),
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
    )
    toastOk('Backup uploaded', 'You can restore it from the list.')
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
</script>

<template>
  <div v-if="data" class="grid gap-4">
    <Card>
      <CardHeader>
        <CardTitle>Saved data</CardTitle>
        <CardDescription>The <span class="font-mono">/data</span> folder keeps its files when you deploy a new version.</CardDescription>
      </CardHeader>
      <CardContent>
        <FormScope :form="dataForm">
          <form class="grid gap-4" novalidate @submit.prevent="submitData">
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

            <FormField v-slot="{ value, handleChange }" name="volume">
              <FormItem class="flex flex-row items-center justify-between gap-4 rounded-lg border p-3">
                <div class="grid gap-1">
                  <FormLabel>Keep files in /data</FormLabel>
                  <FormDescription>Deploys restart the app for a few seconds while this is on. Backups stay if you turn it off.</FormDescription>
                </div>
                <FormControl><Switch :model-value="value" @update:model-value="handleChange" /></FormControl>
              </FormItem>
            </FormField>
            <FormField v-if="dataForm.values.volume" v-slot="{ componentField }" name="volume_gb">
              <FormItem class="sm:max-w-[calc(50%-0.5rem)]">
                <FormLabel>Limit (GB)</FormLabel>
                <FormControl><Input type="number" min="0.1" step="0.5" inputmode="decimal" v-bind="componentField" /></FormControl>
                <FormDescription v-if="storageFree">{{ storageFree }}</FormDescription>
                <FormMessage />
              </FormItem>
            </FormField>
            <div><Button type="submit" :disabled="saveData.isPending.value">{{ saveData.isPending.value ? 'Saving...' : 'Save' }}</Button></div>
          </form>
        </FormScope>
      </CardContent>
    </Card>

    <Card>
      <CardHeader class="flex flex-row flex-wrap items-center justify-between gap-x-4 gap-y-3">
        <div class="grid min-w-0 gap-1">
          <CardTitle>Backups</CardTitle>
          <CardDescription v-if="!data.volume && list?.items.length">Your backups are kept while /data is off.</CardDescription>
          <CardDescription v-else-if="data.volume && list?.items.length">{{ list.items.length }} {{ list.items.length === 1 ? 'backup' : 'backups' }}, {{ formatBytes(list.total_bytes) }} in total.</CardDescription>
          <CardDescription v-else>Copies of /data that you can download or restore.</CardDescription>
        </div>
        <div v-if="data.volume" class="flex flex-wrap gap-2">
          <input ref="fileInput" type="file" accept=".gz,.tgz,application/gzip,application/x-gzip" class="hidden" @change="onFile" />
          <Button variant="outline" :disabled="working" @click="fileInput?.click()">
            <Loader2 v-if="uploading" class="animate-spin" /><Upload v-else />
            {{ uploading ? (processing ? 'Processing...' : `Uploading ${Math.round(progress * 100)}%`) : 'Upload backup' }}
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
          <AlertDescription>{{ list?.restore?.app_restarts ? 'The app restarts when it is done. ' : '' }}This can take a few minutes.</AlertDescription>
        </Alert>
        <Alert v-else-if="failedRestore" variant="destructive">
          <AlertTitle>The backup was not restored.</AlertTitle>
          <AlertDescription>
            <p>{{ failedRestore.error }}</p>
            <button type="button" class="w-fit underline" :disabled="dismissRestore.isPending.value" @click="dismissRestore.mutate()">Dismiss</button>
          </AlertDescription>
        </Alert>

        <FormScope v-if="data.volume && list" :form="backupForm">
          <form class="grid gap-4 sm:grid-cols-2 sm:items-start" novalidate @submit.prevent="submitSettings">
            <FormField v-slot="{ componentField }" name="schedule">
              <FormItem>
                <FormLabel>Automatic backups</FormLabel>
                <Select v-bind="componentField">
                  <FormControl><SelectTrigger class="w-full"><SelectValue /></SelectTrigger></FormControl>
                  <SelectContent>
                    <SelectItem value="off">Off</SelectItem>
                    <SelectItem value="daily">Every night</SelectItem>
                    <SelectItem value="weekly">Every week</SelectItem>
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
                <FormField v-slot="{ componentField }" name="keep_mode">
                  <FormItem class="min-w-0 flex-1">
                    <Select v-bind="componentField">
                      <FormControl><SelectTrigger class="w-full"><SelectValue /></SelectTrigger></FormControl>
                      <SelectContent>
                        <SelectItem value="last">the latest</SelectItem>
                        <SelectItem value="days">backups for</SelectItem>
                      </SelectContent>
                    </Select>
                  </FormItem>
                </FormField>
                <FormField v-slot="{ componentField }" name="keep">
                  <FormItem class="w-20 shrink-0">
                    <FormControl><Input type="number" min="1" step="1" inputmode="numeric" aria-label="How many to keep" v-bind="componentField" /></FormControl>
                  </FormItem>
                </FormField>
                <span class="shrink-0 text-sm text-muted-foreground">{{ backupForm.values.keep_mode === 'days' ? 'days' : 'backups' }}</span>
              </div>
              <p v-if="backupForm.errors.value.keep" class="text-[0.8rem] font-medium text-destructive">{{ backupForm.errors.value.keep }}</p>
              <p v-else class="text-[0.8rem] text-muted-foreground">Manual and uploaded backups stay until you delete them.</p>
            </div>
            <div class="sm:col-span-2">
              <Button type="submit" :disabled="saveSettings.isPending.value || !backupForm.meta.value.dirty">
                {{ saveSettings.isPending.value ? 'Saving...' : 'Save' }}
              </Button>
            </div>
          </form>
        </FormScope>

        <QueryError v-if="backups.isError.value && !list" :error="backups.error.value" @retry="backups.refetch()" />
        <Skeleton v-else-if="!list" class="h-24 w-full" />
        <EmptyState
          v-else-if="!list.items.length && !data.volume"
          :icon="HardDrive"
          title="/data is off."
          description="Turn it on to keep files between deploys and to back them up."
        >
          <Button :disabled="turnOn.isPending.value" @click="turnOn.mutate()">{{ turnOn.isPending.value ? 'Turning on...' : 'Turn on /data' }}</Button>
        </EmptyState>
        <EmptyState
          v-else-if="!list.items.length"
          :icon="Archive"
          title="No backups yet."
          description="Back up now, or turn on automatic backups above."
        />
        <ul v-else class="divide-y rounded-lg border">
          <li v-for="b in list.items" :key="b.id" class="flex flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2.5">
            <div class="flex min-w-0 flex-[1_1_16rem] flex-wrap items-center gap-x-3 gap-y-1">
              <span class="text-sm font-medium"><RelativeTime :date="b.created_at" /></span>
              <span class="hidden text-sm text-muted-foreground sm:inline">{{ formatDateTime(b.created_at) }}</span>
              <Badge variant="secondary">{{ kindLabel[b.kind] }}</Badge>
              <span v-if="b.status === 'running'" class="inline-flex items-center gap-1.5 text-sm text-muted-foreground">
                <Loader2 class="size-3.5 animate-spin" /> Working...
              </span>
              <span v-else-if="b.status === 'failed'" class="min-w-0 text-sm text-destructive">{{ b.error || 'This backup failed.' }}</span>
              <span v-else class="text-sm whitespace-nowrap text-muted-foreground tabular-nums">{{ formatBytes(b.size_bytes) }}</span>
            </div>
            <div class="ml-auto flex gap-1">
              <template v-if="b.status === 'ready'">
                <Button as-child size="sm" variant="ghost">
                  <a :href="paths.appBackupDownload(slug, b.id)" download aria-label="Download this backup"><Download /> Download</a>
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  :disabled="working || !data.volume"
                  :title="data.volume ? undefined : 'Turn on /data to restore'"
                  aria-label="Restore this backup"
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
                aria-label="Delete this backup"
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
      title="Restore this backup?"
      :description="`${appWillRestart ? 'The app restarts. ' : ''}A backup of the current data is made first, so you can undo this.`"
      confirm-label="Restore"
      :destructive="false"
      :loading="restore.isPending.value"
      @confirm="toRestore && restore.mutate(toRestore.id)"
    />
    <ConfirmDialog
      v-model:open="deleteOpen"
      title="Delete this backup?"
      description="It is removed for good."
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
