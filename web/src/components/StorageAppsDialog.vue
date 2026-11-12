<script setup lang="ts">
// What your storage holds: per project, with the apps in it (so a postgres
// shows under the project it belongs to). Each app's files (kept images and
// source), its backups and what its /data holds. Biggest first, like By app
// in Admin, Disk.
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { appRoute, projectRoute } from '@/lib/appLinks'
import { formatMB } from '@/lib/format'
import { storageByProject, type AppStorage } from '@/lib/storage'
import type { Project } from '@/lib/types'

const props = defineProps<{ projects: readonly Project[]; ownerId: string; usedMb: number }>()
const open = defineModel<boolean>('open', { default: false })

const rows = computed(() => storageByProject(props.projects, props.ownerId))
const max = computed(() => Math.max(1, ...rows.value.map((r) => r.usedMB)))

const PARTS = [
  { key: 'files', label: 'App files', color: 'var(--chart-1)' },
  { key: 'backups', label: 'Backups', color: 'var(--chart-3)' },
  { key: 'data', label: 'Saved files (/data)', color: 'var(--chart-2)' },
] as const
type Part = (typeof PARTS)[number]['key']
type Sizes = { filesMB: number | null; backupsMB: number; volumeUsedMB: number }

function size(r: Sizes, p: Part): number {
  if (p === 'files') return r.filesMB ?? 0
  if (p === 'backups') return r.backupsMB
  return r.volumeUsedMB
}

function detail(r: AppStorage): string {
  const out: string[] = []
  if (r.filesMB === null) out.push('files not measured yet')
  else if (r.filesMB > 0) out.push(`files ${formatMB(r.filesMB)}`)
  if (r.backupsMB > 0) out.push(`backups ${formatMB(r.backupsMB)}`)
  if (r.volumeMB > 0) out.push(`/data ${formatMB(r.volumeUsedMB)}`)
  return out.join(' · ')
}

</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="flex max-h-[85vh] flex-col sm:max-w-xl" data-testid="storage-apps">
      <DialogHeader>
        <DialogTitle>Storage by project</DialogTitle>
        <DialogDescription>{{ formatMB(usedMb) }} in use: app files, backups and saved files (/data).</DialogDescription>
      </DialogHeader>

      <p v-if="rows.length === 0" class="py-6 text-center text-sm text-muted-foreground">You have no apps yet.</p>
      <ul v-else class="-mx-4 grid min-h-0 gap-5 overflow-y-auto px-4 py-1">
        <li v-for="p in rows" :key="p.project.id" class="grid gap-2" :data-testid="`storage-project-${p.project.id}`">
          <div class="grid gap-1.5">
            <div class="flex items-baseline gap-3">
              <RouterLink :to="projectRoute(p.project.id)" class="min-w-0 truncate text-sm font-medium hover:underline" @click="open = false">{{ p.project.name }}</RouterLink>
              <span class="ml-auto text-sm font-medium whitespace-nowrap tabular-nums">{{ formatMB(p.usedMB) }}</span>
            </div>
            <div class="flex h-2 overflow-hidden rounded-full bg-muted">
              <div class="flex h-full gap-px" :style="{ width: `${(p.usedMB / max) * 100}%` }">
                <div
                  v-for="part in PARTS"
                  v-show="size(p, part.key) > 0"
                  :key="part.key"
                  class="h-full"
                  :style="{ flexGrow: size(p, part.key), background: part.color }"
                  :title="`${part.label}: ${formatMB(size(p, part.key))}`"
                />
              </div>
            </div>
          </div>
          <ul class="grid gap-1 border-l pl-3">
            <li v-for="a in p.apps" :key="a.app.id" class="flex items-baseline gap-3 text-xs" :data-testid="`storage-app-${a.app.slug}`">
              <RouterLink :to="appRoute(a.app, 'app-storage')" class="max-w-[45%] min-w-0 shrink-0 truncate font-medium text-foreground hover:underline" @click="open = false">
                {{ a.app.name || a.app.slug }}
              </RouterLink>
              <span class="min-w-0 flex-1 truncate text-muted-foreground">{{ detail(a) }}</span>
              <span class="whitespace-nowrap text-muted-foreground tabular-nums">{{ formatMB(a.usedMB) }}</span>
            </li>
          </ul>
        </li>
      </ul>

      <p class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span v-for="part in PARTS" :key="part.key" class="inline-flex items-center gap-1.5">
          <span class="size-2 rounded-[3px]" :style="{ background: part.color }" />{{ part.label }}
        </span>
      </p>
    </DialogContent>
  </Dialog>
</template>
