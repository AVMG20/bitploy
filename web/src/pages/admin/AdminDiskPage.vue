<script setup lang="ts">
// Admin > Server > Disk: where the disk space goes, cleanups that free it,
// and the automatic cleanup. Measuring sizes every image and volume, so the
// report loads on open and on Refresh only (the server reuses it for 2 minutes).
import { computed, onBeforeUnmount, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery } from '@tanstack/vue-query'
import { ArrowLeft, Box, Check, Database, Hammer, History, Layers, Package, RefreshCw } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import PageHeader from '@/components/PageHeader.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { CLEANUPS, share } from '@/lib/disk'
import { formatBytes } from '@/lib/format'
import { toastError, toastLoading } from '@/lib/notify'
import type { DiskCleanupKind, DiskCleanupOption } from '@/lib/types'
import { cn } from '@/lib/utils'
import AdminTabBar from './AdminTabBar.vue'
import DiskApps from './disk/DiskApps.vue'
import DiskAutoCleanup from './disk/DiskAutoCleanup.vue'
import DiskDetails from './disk/DiskDetails.vue'
import DiskSettings from './disk/DiskSettings.vue'
import DiskUsage from './disk/DiskUsage.vue'

let refreshNext = false
const report = useQuery({
  queryKey: keys.adminDisk,
  queryFn: () => {
    const refresh = refreshNext
    refreshNext = false
    return api.adminDisk(refresh)
  },
  staleTime: 60_000,
  refetchOnWindowFocus: false,
})
const r = computed(() => report.data.value)
function refresh() {
  refreshNext = true
  void report.refetch()
}

const ICONS: Record<DiskCleanupKind, typeof Box> = {
  build_cache: Hammer,
  unused_images: Package,
  stopped_containers: Box,
  rollback_images: History,
  other_images: Layers,
  orphan_volumes: Database,
}

// What a cleanup freed shows in the free space right away, before the new
// measurement comes in.
const extraFree = ref(0)
const freed = ref(0)
const done = ref<{ kind: DiskCleanupKind; bytes: number } | null>(null)
let flashTimer: ReturnType<typeof setTimeout> | undefined
onBeforeUnmount(() => clearTimeout(flashTimer))

const freeBytes = computed(() => (r.value ? Math.min(r.value.filesystem.total_bytes, r.value.filesystem.free_bytes + extraFree.value) : 0))
const freePercent = computed(() => (r.value ? share(freeBytes.value, r.value.filesystem.total_bytes) : 100))

const pending = ref<DiskCleanupOption | null>(null)
const confirmOpen = ref(false)
function ask(o: DiskCleanupOption) {
  pending.value = o
  confirmOpen.value = true
}
const info = computed(() => (pending.value ? CLEANUPS[pending.value.kind] : null))

const cleanup = useMutation({
  mutationFn: (o: DiskCleanupOption) => api.adminDiskCleanup({ kind: o.kind, volumes: o.kind === 'orphan_volumes' ? o.volumes : undefined }),
  // The dialog closes at once; a loading toast follows the cleanup.
  onMutate: (o) => {
    confirmOpen.value = false
    return toastLoading(`${CLEANUPS[o.kind].confirmLabel}...`)
  },
  onSuccess: async (res, _o, t) => {
    const c = CLEANUPS[res.kind]
    const what = res.removed > 0 && c.unit(res.removed) ? `Deleted ${c.unit(res.removed)}.` : undefined
    if (res.error) t.warn(`Freed ${formatBytes(res.freed_bytes)}; part of the cleanup failed`, res.error)
    else if (res.freed_bytes > 0) t.ok(`Freed ${formatBytes(res.freed_bytes)}`, what)
    else t.ok('Nothing to free', what)
    done.value = { kind: res.kind, bytes: res.freed_bytes }
    extraFree.value += res.freed_bytes
    freed.value = res.freed_bytes
    clearTimeout(flashTimer)
    flashTimer = setTimeout(() => {
      freed.value = 0
      done.value = null
    }, 5000)
    await report.refetch()
    extraFree.value = 0
  },
  onError: (e, _o, t) => (t ? t.fail(e) : toastError(e)),
})

function blockedReason(o: DiskCleanupOption): string {
  if (o.kind === 'other_images' && r.value?.auto.dev) return 'Off in dev mode'
  if (o.bytes <= 0 && o.count === 0) return 'Nothing to delete'
  return ''
}
const reclaimable = computed(() => (r.value?.cleanups ?? []).filter((o) => !blockedReason(o)).reduce((n, o) => n + o.bytes, 0))
const pausedNow = computed(() => !!r.value && r.value.filesystem.total_bytes > 0 && freePercent.value < r.value.builds_paused_percent)
</script>

<template>
  <div class="grid min-w-0 gap-5">
    <PageHeader title="Admin" />
    <AdminTabBar tab="server" page />

    <div class="flex flex-wrap items-center justify-between gap-3 pt-1">
      <div class="flex items-center gap-2">
        <Button as-child variant="ghost" size="icon" class="size-8" aria-label="Back to Server">
          <RouterLink :to="{ name: 'admin', query: { tab: 'server' } }"><ArrowLeft /></RouterLink>
        </Button>
        <h2 class="text-lg font-semibold tracking-tight">Disk</h2>
      </div>
      <div class="flex items-center gap-3 text-xs text-muted-foreground">
        <span v-if="r">Measured <RelativeTime :date="r.measured_at" /></span>
        <Button variant="outline" size="sm" :disabled="report.isFetching.value || cleanup.isPending.value" data-testid="disk-refresh" @click="refresh">
          <RefreshCw :class="report.isFetching.value && 'animate-spin'" /> {{ report.isFetching.value ? 'Measuring...' : 'Refresh' }}
        </Button>
      </div>
    </div>

    <QueryError v-if="report.isError.value && !r" :error="report.error.value" @retry="refresh" />

    <template v-else-if="!r">
      <Card><CardContent class="grid gap-5"><Skeleton class="h-10 w-56" /><Skeleton class="h-3.5 w-full" /><Skeleton class="h-16 w-full" /></CardContent></Card>
      <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3"><Skeleton v-for="i in 3" :key="i" class="h-40 rounded-xl" /></div>
    </template>

    <template v-else>
      <Alert v-if="pausedNow" variant="destructive">
        <AlertTitle>Builds, uploads and backups are paused</AlertTitle>
        <AlertDescription>They start again above {{ r.builds_paused_percent }}% free. Running apps keep running.</AlertDescription>
      </Alert>
      <Alert v-if="!r.docker_available">
        <AlertTitle>bitploy cannot reach Docker</AlertTitle>
        <AlertDescription>{{ r.docker_error || 'Images, containers and volumes are not measured.' }}</AlertDescription>
      </Alert>

      <Card>
        <CardContent class="py-2">
          <DiskUsage
            :total-bytes="r.filesystem.total_bytes"
            :free-bytes="freeBytes"
            :segments="r.segments"
            :auto-percent="r.auto.enabled && !r.auto.dev ? r.auto.min_free_percent : null"
            :paused-percent="r.builds_paused_percent"
            :warn-percent="r.auto.warn_free_percent > 0 ? r.auto.warn_free_percent : null"
            :freed="freed"
          />
        </CardContent>
      </Card>

      <section v-if="r.cleanups.length" class="grid gap-3">
        <div class="flex items-baseline justify-between gap-3">
          <h3 class="text-sm font-semibold">Free up space</h3>
          <span v-if="reclaimable > 0" class="text-xs text-muted-foreground">up to <span class="font-medium text-foreground tabular-nums">{{ formatBytes(reclaimable) }}</span></span>
        </div>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Card
            v-for="o in r.cleanups"
            :key="o.kind"
            :class="cn('gap-3 transition-colors', !blockedReason(o) && 'hover:border-primary/40', done?.kind === o.kind && 'border-success/50')"
            :data-testid="`disk-cleanup-${o.kind}`"
          >
            <CardContent class="flex h-full flex-col gap-3">
              <div class="flex items-center gap-2.5">
                <span
                  class="grid size-8 place-items-center rounded-lg"
                  :class="CLEANUPS[o.kind].destructive ? 'bg-destructive/10 text-destructive' : 'bg-primary/10 text-primary'"
                >
                  <component :is="ICONS[o.kind]" class="size-4" />
                </span>
                <span class="font-medium">{{ CLEANUPS[o.kind].title }}</span>
              </div>
              <div>
                <p class="text-2xl font-semibold tracking-tight tabular-nums" :class="blockedReason(o) && 'text-muted-foreground'">{{ formatBytes(Math.max(0, o.bytes)) }}</p>
                <p v-if="CLEANUPS[o.kind].unit(o.count)" class="text-xs text-muted-foreground">{{ CLEANUPS[o.kind].unit(o.count) }}</p>
              </div>
              <p class="flex-1 text-xs text-muted-foreground">{{ CLEANUPS[o.kind].description }}</p>
              <p v-if="done?.kind === o.kind" class="inline-flex h-8 items-center gap-1.5 text-sm font-medium text-success" role="status">
                <Check class="size-4" /> Freed {{ formatBytes(done.bytes) }}
              </p>
              <p v-else-if="blockedReason(o)" class="inline-flex h-8 items-center text-sm text-muted-foreground">{{ blockedReason(o) }}</p>
              <Button
                v-else
                size="sm"
                class="self-start"
                :variant="CLEANUPS[o.kind].destructive ? 'outline' : 'secondary'"
                :disabled="cleanup.isPending.value"
                @click="ask(o)"
              >
                {{ cleanup.isPending.value && pending?.kind === o.kind ? 'Deleting...' : CLEANUPS[o.kind].confirmLabel }}
              </Button>
            </CardContent>
          </Card>
        </div>
      </section>

      <DiskApps v-if="r.docker_available" :apps="r.apps" />

      <div class="grid items-start gap-5 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        <DiskAutoCleanup :auto="r.auto" :cleanups="r.cleanups" :free-percent="freePercent" />
        <DiskSettings :auto="r.auto" />
      </div>

      <DiskDetails v-if="r.docker_available || r.files.length" :report="r" />
    </template>

    <ConfirmDialog
      v-if="pending && info"
      v-model:open="confirmOpen"
      :title="`${info.confirmLabel}?`"
      :description="info.confirm(formatBytes(pending.bytes), pending.count)"
      :confirm-label="info.confirmLabel"
      :destructive="info.destructive"
      :type-to-confirm="pending.kind === 'orphan_volumes' ? 'delete volumes' : undefined"
      @confirm="cleanup.mutate(pending)"
    >
      <ul v-if="pending.kind === 'orphan_volumes' && pending.volumes?.length" class="max-h-40 overflow-y-auto rounded-md border bg-muted/30 px-3 py-2 font-mono text-xs">
        <li v-for="v in pending.volumes" :key="v" class="truncate py-0.5">{{ v }}</li>
      </ul>
    </ConfirmDialog>
  </div>
</template>
