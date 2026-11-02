<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { ChevronRight, Download, File as FileIcon, Folder, FolderPlus, Link as LinkIcon, Loader2, Trash2, Upload } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { keys } from '@/composables/queries'
import { ApiError, api } from '@/lib/api'
import { formatBytes, formatDateTime } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import type { DataFile } from '@/lib/types'

// A small file browser for /data: look around, download a file (a database, a
// JSON file), upload a file into a folder, make a folder, delete.
const props = defineProps<{ slug: string; running: boolean }>()
const qc = useQueryClient()

const path = ref('') // the open folder, relative to /data
const crumbs = computed(() => {
  const parts = path.value ? path.value.split('/') : []
  return parts.map((name, i) => ({ name, path: parts.slice(0, i + 1).join('/') }))
})
const child = (name: string) => (path.value ? `${path.value}/${name}` : name)

const files = useQuery({
  queryKey: computed(() => ['files', props.slug, path.value] as const),
  queryFn: () => api.files(props.slug, path.value),
  placeholderData: keepPreviousData,
  retry: false,
})
const list = computed(() => files.data.value)
const loadingFolder = computed(() => files.isPlaceholderData.value)
const notCreatedYet = computed(() => files.error.value instanceof ApiError && files.error.value.code === 'volume_not_ready')

// The open folder was deleted or is a file now: go back to the top.
watch(
  () => files.error.value,
  (e) => {
    if (e instanceof ApiError && (e.code === 'file_not_found' || e.code === 'not_a_folder') && path.value) path.value = ''
  },
)

function refresh() {
  void qc.invalidateQueries({ queryKey: ['files', props.slug] })
  void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
}

const hasDatabase = computed(() => !!list.value?.items.some((f) => f.type === 'file' && /\.(db|sqlite|sqlite3)$/i.test(f.name)))

function icon(f: DataFile) {
  return f.type === 'dir' ? Folder : f.type === 'symlink' ? LinkIcon : FileIcon
}

// ---------------------------------------------------------------- upload
const fileInput = ref<HTMLInputElement | null>(null)
const uploading = ref(false)
const progress = ref(0)
const uploadName = ref('')
const busy = computed(() => uploading.value || remove.isPending.value || makeFolder.isPending.value)

const overwriteName = ref<string | null>(null)
let overwriteAnswer: ((yes: boolean) => void) | null = null
function askOverwrite(name: string): Promise<boolean> {
  overwriteName.value = name
  return new Promise((resolve) => (overwriteAnswer = resolve))
}
function answerOverwrite(yes: boolean) {
  overwriteName.value = null
  overwriteAnswer?.(yes)
  overwriteAnswer = null
}
const overwriteOpen = computed({
  get: () => overwriteName.value !== null,
  set: (v) => {
    if (!v) answerOverwrite(false)
  },
})

async function uploadAll(chosen: File[]) {
  if (busy.value || !chosen.length) return
  // The folder and its items as they were when the upload started: the list
  // may show another folder by the time a later file is sent.
  const folder = path.value
  const max = list.value?.upload_max_bytes
  const items = list.value?.items ?? []
  let done = 0
  uploading.value = true
  try {
    for (const file of chosen) {
      if (max !== undefined && file.size > max) {
        toastError(new Error(`${file.name} is too big. Files can be up to ${formatBytes(max)} here.`))
        continue
      }
      const existing = items.find((f) => f.name === file.name)
      if (existing?.type === 'dir') {
        toastError(new Error(`There is a folder called ${file.name} here.`))
        continue
      }
      let overwrite = false
      if (existing) {
        if (!(await askOverwrite(file.name))) continue
        overwrite = true
      }
      uploadName.value = file.name
      progress.value = 0
      try {
        try {
          await api.uploadDataFile(props.slug, folder, file, overwrite, (f) => (progress.value = f))
        } catch (e) {
          // The folder has more items than the list shows: the server knows.
          if (!(e instanceof ApiError && e.code === 'file_exists' && !overwrite && (await askOverwrite(file.name)))) throw e
          await api.uploadDataFile(props.slug, folder, file, true, (f) => (progress.value = f))
        }
        done++
      } catch (e) {
        toastError(e)
      }
    }
  } finally {
    uploading.value = false
    uploadName.value = ''
    if (done) {
      toastOk(done === 1 ? 'File uploaded' : `${done} files uploaded`)
      refresh()
    }
  }
}

function onPick(e: Event) {
  const input = e.target as HTMLInputElement
  const chosen = Array.from(input.files ?? [])
  input.value = '' // so the same file can be picked again
  void uploadAll(chosen)
}

const dragging = ref(false)
let dragDepth = 0
const hasFiles = (e: DragEvent) => !!e.dataTransfer && Array.from(e.dataTransfer.types).includes('Files')
function onDragEnter(e: DragEvent) {
  if (!hasFiles(e)) return
  dragDepth++
  dragging.value = true
}
function onDragLeave() {
  dragDepth = Math.max(0, dragDepth - 1)
  if (!dragDepth) dragging.value = false
}
function onDrop(e: DragEvent) {
  dragDepth = 0
  dragging.value = false
  const dt = e.dataTransfer
  if (!dt) return
  const items = Array.from(dt.items ?? [])
  if (items.some((i) => i.webkitGetAsEntry?.()?.isDirectory)) {
    toastError(new Error('Folders cannot be uploaded. Drop the files in it instead.'))
    return
  }
  void uploadAll(Array.from(dt.files))
}

// ---------------------------------------------------------------- new folder
const folderOpen = ref(false)
const folderName = ref('')
const makeFolder = useMutation({
  mutationFn: (name: string) => api.makeFolder(props.slug, path.value, name),
  onSuccess: () => {
    folderOpen.value = false
    refresh()
  },
  onError: (e) => toastError(e),
})
function openFolderDialog() {
  folderName.value = ''
  folderOpen.value = true
}
function submitFolder() {
  const name = folderName.value.trim()
  if (name) makeFolder.mutate(name)
}

// ---------------------------------------------------------------- delete
const toDelete = ref<DataFile | null>(null)
const deleteOpen = computed({
  get: () => toDelete.value !== null,
  set: (v) => {
    if (!v) toDelete.value = null
  },
})
const remove = useMutation({
  mutationFn: (f: DataFile) => api.deleteDataFile(props.slug, child(f.name)),
  onSuccess: (_, f) => {
    toastOk(f.type === 'dir' ? 'Folder deleted' : 'File deleted')
    toDelete.value = null
    refresh()
  },
  onError: (e) => {
    toDelete.value = null
    toastError(e)
  },
})
</script>

<template>
  <Card
    class="relative transition-shadow"
    :class="dragging && 'ring-2 ring-primary'"
    @dragenter.prevent="onDragEnter"
    @dragover.prevent
    @dragleave.prevent="onDragLeave"
    @drop.prevent="onDrop"
  >
    <CardHeader class="flex flex-row flex-wrap items-center justify-between gap-x-4 gap-y-3">
      <div class="grid min-w-0 gap-1">
        <CardTitle>Files</CardTitle>
        <CardDescription>Download a file from <span class="font-mono">/data</span>, or upload one to replace it.</CardDescription>
      </div>
      <div v-if="list" class="flex flex-wrap gap-2">
        <input ref="fileInput" type="file" multiple class="hidden" @change="onPick" />
        <Button variant="outline" :disabled="busy" @click="openFolderDialog"><FolderPlus /> New folder</Button>
        <Button :disabled="busy" @click="fileInput?.click()">
          <Loader2 v-if="uploading" class="animate-spin" /><Upload v-else />
          {{ uploading ? `Uploading ${Math.round(progress * 100)}%` : 'Upload' }}
        </Button>
      </div>
    </CardHeader>
    <CardContent class="grid gap-3">
      <template v-if="files.isError.value && !list && !notCreatedYet">
        <QueryError :error="files.error.value" @retry="files.refetch()" />
        <div v-if="path">
          <Button variant="outline" size="sm" @click="path = ''">Back to /data</Button>
        </div>
      </template>
      <EmptyState
        v-else-if="notCreatedYet"
        :icon="Folder"
        title="No /data folder yet."
        description="Restart or redeploy the app to create it."
      />
      <Skeleton v-else-if="!list" class="h-40 w-full" />
      <template v-else>
        <nav class="flex flex-wrap items-center gap-x-1 gap-y-1 text-sm" aria-label="Folder">
          <button
            type="button"
            class="rounded px-1 py-0.5 font-mono hover:bg-muted"
            :class="crumbs.length ? 'text-muted-foreground' : 'font-semibold'"
            :disabled="!crumbs.length"
            @click="path = ''"
          >
            /data
          </button>
          <template v-for="(c, i) in crumbs" :key="c.path">
            <ChevronRight class="size-3.5 text-muted-foreground" />
            <button
              type="button"
              class="rounded px-1 py-0.5 font-mono hover:bg-muted"
              :class="i < crumbs.length - 1 ? 'text-muted-foreground' : 'font-semibold'"
              :disabled="i === crumbs.length - 1"
              @click="path = c.path"
            >
              {{ c.name }}
            </button>
          </template>
          <Loader2 v-if="loadingFolder" class="ml-1 size-3.5 animate-spin text-muted-foreground" />
        </nav>

        <EmptyState v-if="!list.items.length" :icon="Folder" title="This folder is empty." description="Upload a file or drop one here." />
        <div v-else class="overflow-hidden rounded-lg border" :class="loadingFolder && 'opacity-60'">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead class="text-right">Size</TableHead>
                <TableHead class="hidden sm:table-cell">Modified</TableHead>
                <TableHead class="w-px"><span class="sr-only">Actions</span></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="f in list.items" :key="f.name">
                <TableCell class="max-w-0 min-w-40 whitespace-normal">
                  <button
                    v-if="f.type === 'dir'"
                    type="button"
                    class="flex w-full items-center gap-2 text-left font-medium hover:underline"
                    :disabled="loadingFolder"
                    @click="path = child(f.name)"
                  >
                    <component :is="icon(f)" class="size-4 shrink-0 text-muted-foreground" />
                    <span class="min-w-0 break-all">{{ f.name }}</span>
                  </button>
                  <span v-else class="flex items-center gap-2">
                    <component :is="icon(f)" class="size-4 shrink-0 text-muted-foreground" />
                    <span class="min-w-0 break-all">{{ f.name }}</span>
                    <span v-if="f.type === 'symlink'" class="shrink-0 text-xs text-muted-foreground">link</span>
                    <span v-else-if="f.type === 'other'" class="shrink-0 text-xs text-muted-foreground">special</span>
                  </span>
                </TableCell>
                <TableCell class="text-right text-muted-foreground tabular-nums">{{ f.type === 'file' ? formatBytes(f.size) : '' }}</TableCell>
                <TableCell class="hidden text-muted-foreground sm:table-cell" :title="formatDateTime(f.modified_at)">
                  <RelativeTime :date="f.modified_at" />
                </TableCell>
                <TableCell class="text-right">
                  <div class="flex justify-end gap-1">
                    <Button v-if="f.type === 'file'" as-child size="icon-sm" variant="ghost" title="Download">
                      <a :href="api.dataFileDownloadUrl(slug, child(f.name))" download aria-label="Download this file"><Download /></a>
                    </Button>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      class="text-muted-foreground hover:text-destructive"
                      :disabled="busy"
                      title="Delete"
                      aria-label="Delete"
                      @click="toDelete = f"
                    >
                      <Trash2 />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>

        <p v-if="list.truncated" class="text-sm text-muted-foreground">Showing the first {{ list.items.length }} items of this folder.</p>
        <p v-if="running && hasDatabase" class="text-sm text-muted-foreground">
          Stop the app before you replace a database file, or the app may overwrite it. A download while the app runs can miss recent changes.
        </p>
        <p v-if="uploading" class="text-sm text-muted-foreground">Uploading {{ uploadName }}...</p>
      </template>
    </CardContent>

    <div v-if="dragging" class="pointer-events-none absolute inset-0 flex items-center justify-center rounded-xl bg-background/80 text-sm font-medium">
      Drop to upload to /data{{ path ? '/' + path : '' }}
    </div>
  </Card>

  <Dialog v-model:open="folderOpen">
    <DialogContent class="sm:max-w-sm">
      <form class="grid gap-4" @submit.prevent="submitFolder">
        <DialogHeader>
          <DialogTitle>New folder</DialogTitle>
          <DialogDescription>In <span class="font-mono">/data{{ path ? '/' + path : '' }}</span>.</DialogDescription>
        </DialogHeader>
        <Input v-model="folderName" aria-label="Folder name" placeholder="Folder name" autocomplete="off" autocapitalize="off" spellcheck="false" />
        <DialogFooter>
          <Button type="button" variant="outline" :disabled="makeFolder.isPending.value" @click="folderOpen = false">Cancel</Button>
          <Button type="submit" :disabled="!folderName.trim() || makeFolder.isPending.value">
            {{ makeFolder.isPending.value ? 'Creating...' : 'Create' }}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>

  <ConfirmDialog
    v-model:open="overwriteOpen"
    :title="`Replace ${overwriteName ?? 'this file'}?`"
    description="A file with this name already exists. The new one replaces it."
    confirm-label="Replace"
    @confirm="answerOverwrite(true)"
  />
  <ConfirmDialog
    v-model:open="deleteOpen"
    :title="`Delete ${toDelete?.name ?? ''}?`"
    :description="toDelete?.type === 'dir' ? 'The folder and everything in it are deleted. This cannot be undone.' : 'This cannot be undone.'"
    confirm-label="Delete"
    :loading="remove.isPending.value"
    @confirm="toDelete && remove.mutate(toDelete)"
  />
</template>
