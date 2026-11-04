<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { CircleCheck, FileArchive, FileCode, FolderUp, TriangleAlert, X } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { detectSource, guessHomePage, NEXT_CONFIG_NAMES } from '@/lib/detect'
import { formatBytes } from '@/lib/format'
import type { BuildMode } from '@/lib/types'
import {
  buildZip,
  checkRawLimits,
  collectFromDrop,
  collectFromFileList,
  listZipNames,
  readZipText,
  MAX_FILES,
  MAX_UNPACKED_BYTES,
  MAX_ZIP_BYTES,
  serverLayout,
  zipNameAtTop,
  ZipTooBigError,
} from '@/lib/zip'
import type { Collected } from '@/lib/zip'

// A drop zone for a project folder, a .zip or a single .html page: reads it,
// packs a folder or page into a zip in the browser, checks the size limits and
// says what bitploy will make of it. v-model is the zip that is ready to send
// (null until one is chosen).

export interface UploadChoice {
  /** The zip to send. */
  zip: Blob
  /** "my-site", "site.zip" or "Planning tool.html". */
  name: string
  from: 'folder' | 'zip' | 'page'
  fileCount: number | null
  /** File paths in the project, wrapping folder removed; null when a zip could not be read here. */
  paths: string[] | null
  /** The text of package.json at the top of the project; null when there is none or it could not be read here. */
  packageJson: string | null
  /** The text of the Next.js config at the top of the project; null when there is none or it could not be read here. */
  nextConfig: string | null
  skipped: string[]
}

const props = defineProps<{ disabled?: boolean; /** The output folder chosen for a static build; empty: the detected one. */ outputDir?: string }>()
const choice = defineModel<UploadChoice | null>({ default: null })
/** How the source is read: auto, or the type the person chose. */
const mode = defineModel<BuildMode>('mode', { default: 'auto' })
/** The page for "/" when there are several pages and no index.html. */
const homePage = defineModel<string>('homePage', { default: '' })

const MB = 1024 * 1024
const MAX_PACKAGE_JSON = 1 << 20

/** The text of the first Next.js config in the project, read from a folder. */
async function folderNextConfig(files: Collected['files']): Promise<string | null> {
  for (const name of NEXT_CONFIG_NAMES) {
    const f = files.find((x) => x.path === name)
    if (f) return f.file.size <= MAX_PACKAGE_JSON ? await f.file.text().catch(() => null) : null
  }
  return null
}

/** The same for a zip, whose entries may sit in one wrapping folder. */
async function zipNextConfig(file: File, names: string[] | null, kept: string[] | null): Promise<string | null> {
  if (!names || !kept) return null
  for (const name of NEXT_CONFIG_NAMES) {
    if (!kept.includes(name)) continue
    const entry = zipNameAtTop(names, name)
    return entry ? await readZipText(file, entry, MAX_PACKAGE_JSON) : null
  }
  return null
}
const folderInput = ref<HTMLInputElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
const over = ref(false)
const error = ref('')
const packing = ref<{ done: number; total: number } | null>(null)
let abort: AbortController | null = null

const detected = computed(() =>
  choice.value?.paths ? detectSource(choice.value.paths, mode.value, homePage.value, 'Dockerfile', choice.value.packageJson, choice.value.nextConfig) : null,
)
const homeChoices = computed(() => (detected.value?.kind === 'none' || detected.value?.kind === 'static' ? (detected.value.choices ?? null) : null))
// Several pages: start from a guess (or the page chosen before, when the new
// files still have it), so the person only changes it when it is wrong.
watch(homeChoices, (pages) => {
  if (pages && !pages.includes(homePage.value)) homePage.value = guessHomePage(pages)
})

function tooBig(e: ZipTooBigError): string {
  const leave = 'Leave out node_modules, build output, videos and other big files, then try again.'
  if (e.kind === 'files') return `This folder has ${e.bytes.toLocaleString('en')} files. The limit is ${MAX_FILES.toLocaleString('en')}. ${leave}`
  if (e.kind === 'unpacked') return `This folder holds ${formatBytes(e.bytes)}. The limit is ${formatBytes(MAX_UNPACKED_BYTES)}. ${leave}`
  return `This folder is ${formatBytes(e.bytes)} after zipping. The limit is ${Math.round(MAX_ZIP_BYTES / MB)} MB. ${leave}`
}

function reset() {
  abort?.abort()
  abort = null
  packing.value = null
  choice.value = null
  error.value = ''
}

async function takeFolder(c: Collected) {
  if (!c.files.length) {
    error.value = c.skipped.length
      ? `Nothing to upload: only ${c.skipped.join(', ')} found, and those are always left out.`
      : 'That folder is empty. Drop the folder that holds your project.'
    return
  }
  try {
    checkRawLimits(c.files)
  } catch (e) {
    error.value = e instanceof ZipTooBigError ? tooBig(e) : 'Could not read that folder.'
    return
  }
  abort = new AbortController()
  const signal = abort.signal
  packing.value = { done: 0, total: c.files.length }
  try {
    const zip = await buildZip(c.files, (p) => (packing.value = p), signal)
    const pkg = c.files.find((f) => f.path === 'package.json')
    const packageJson = pkg && pkg.file.size <= MAX_PACKAGE_JSON ? await pkg.file.text().catch(() => null) : null
    const nextConfig = await folderNextConfig(c.files)
    const page = !c.root && c.files.length === 1 && isPage(c.files[0]!.path)
    choice.value = {
      zip,
      name: c.root || (c.files.length === 1 ? c.files[0]!.path : 'Your files'),
      from: page ? 'page' : 'folder',
      fileCount: c.files.length,
      // The server removes a single top folder from the zip once more.
      paths: serverLayout(c.files.map((f) => f.path)),
      packageJson,
      nextConfig,
      skipped: c.skipped,
    }
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') return
    error.value = e instanceof ZipTooBigError ? tooBig(e) : 'Could not pack these files. Try a .zip instead.'
  } finally {
    packing.value = null
  }
}

async function takeZip(file: File) {
  if (file.size > MAX_ZIP_BYTES) {
    error.value = `This zip is ${formatBytes(file.size)}. The limit is ${Math.round(MAX_ZIP_BYTES / MB)} MB. Leave out node_modules, build output and videos, zip it again and try again.`
    return
  }
  const names = await listZipNames(file)
  const kept = names ? serverLayout(names) : null
  if (kept && !kept.length) {
    error.value = 'That zip is empty.'
    return
  }
  const pkgName = names && kept?.includes('package.json') ? zipNameAtTop(names, 'package.json') : null
  const packageJson = pkgName ? await readZipText(file, pkgName, MAX_PACKAGE_JSON) : null
  const nextConfig = await zipNextConfig(file, names, kept)
  choice.value = {
    zip: file,
    name: file.name,
    from: 'zip',
    fileCount: kept ? kept.length : null,
    paths: kept,
    packageJson,
    nextConfig,
    skipped: [],
  }
}

function isPage(name: string): boolean {
  return /\.html?$/i.test(name)
}

async function take(run: () => Promise<void>) {
  reset()
  await run()
}

async function onDrop(e: DragEvent) {
  over.value = false
  if (props.disabled || !e.dataTransfer) return
  const dt = e.dataTransfer
  const only = dt.files.length === 1 ? dt.files[0] : undefined
  if (only && /\.zip$/i.test(only.name)) {
    await take(() => takeZip(only))
    return
  }
  // The entries must be taken from the event before the first await.
  const collecting = collectFromDrop(dt)
  await take(async () => takeFolder(await collecting))
}

async function onFolderPicked(e: Event) {
  const input = e.target as HTMLInputElement
  const list = input.files ? Array.from(input.files) : []
  input.value = ''
  if (list.length) await take(async () => takeFolder(collectFromFileList(list)))
}

async function onFilePicked(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (/\.zip$/i.test(file.name)) await take(() => takeZip(file))
  else if (isPage(file.name)) await take(async () => takeFolder(collectFromFileList([file])))
  else {
    reset()
    error.value = `${file.name} is not a .zip or an .html file. To upload a whole project, choose its folder.`
  }
}

// The parent clears the choice (a new start): forget what is shown too.
watch(choice, (c) => {
  if (c === null && !packing.value) error.value = ''
})
onBeforeUnmount(() => abort?.abort())
</script>

<template>
  <div class="grid min-w-0 gap-3">
    <div v-if="packing" class="grid gap-2 rounded-lg border p-4">
      <p class="text-sm font-medium">Getting your files ready...</p>
      <Progress :model-value="(packing.done / Math.max(packing.total, 1)) * 100" />
      <p class="text-xs text-muted-foreground">{{ packing.done.toLocaleString('en') }} of {{ packing.total.toLocaleString('en') }} files</p>
    </div>

    <div
      v-else-if="choice"
      class="flex min-w-0 items-center gap-3 rounded-lg border bg-card p-3"
    >
      <div class="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-muted/50">
        <FileArchive v-if="choice.from === 'zip'" class="size-4" />
        <FileCode v-else-if="choice.from === 'page'" class="size-4" />
        <FolderUp v-else class="size-4" />
      </div>
      <div class="min-w-0 flex-1">
        <p class="truncate text-sm font-medium">{{ choice.name }}</p>
        <p class="text-xs text-muted-foreground">
          <template v-if="choice.fileCount !== null">{{ choice.fileCount.toLocaleString('en') }} {{ choice.fileCount === 1 ? 'file' : 'files' }}, </template>{{ formatBytes(choice.zip.size) }}<template v-if="choice.from !== 'zip'"> zipped</template>
        </p>
      </div>
      <Button type="button" size="sm" variant="ghost" :disabled="disabled" @click="reset"><X /> Remove</Button>
    </div>

    <div
      v-else
      class="flex flex-col items-center gap-3 rounded-lg border border-dashed px-4 py-8 text-center transition-colors"
      :class="[over ? 'border-primary bg-primary/5' : 'bg-card/40', disabled && 'pointer-events-none opacity-60']"
      @dragenter.prevent="over = true"
      @dragover.prevent="over = true"
      @dragleave.prevent="over = false"
      @drop.prevent="onDrop"
    >
      <div class="flex size-10 items-center justify-center rounded-lg border bg-card text-muted-foreground shadow-card">
        <FolderUp class="size-5" />
      </div>
      <div>
        <p class="text-sm font-medium">Drop your project folder here</p>
        <p class="mt-1 text-xs text-muted-foreground">or a .zip, or a single .html page, up to {{ Math.round(MAX_ZIP_BYTES / MB) }} MB</p>
      </div>
      <div class="flex flex-wrap justify-center gap-2">
        <Button type="button" size="sm" variant="outline" :disabled="disabled" @click="folderInput?.click()">Choose a folder</Button>
        <Button type="button" size="sm" variant="outline" :disabled="disabled" @click="fileInput?.click()">Choose a file</Button>
      </div>
    </div>

    <input ref="folderInput" type="file" class="hidden" webkitdirectory multiple tabindex="-1" aria-hidden="true" @change="onFolderPicked" />
    <input
      ref="fileInput"
      type="file"
      class="hidden"
      accept=".zip,.html,.htm,application/zip,application/x-zip-compressed,text/html"
      tabindex="-1"
      aria-hidden="true"
      @change="onFilePicked"
    />

    <p v-if="error" class="flex items-start gap-2 rounded-lg bg-destructive/10 p-3 text-sm text-destructive" role="alert">
      <TriangleAlert class="mt-0.5 size-4 shrink-0" />
      <span>{{ error }}</span>
    </p>

    <template v-if="choice && !packing">
      <p v-if="choice.skipped.length" class="text-xs text-muted-foreground">
        Left out: {{ choice.skipped.join(', ') }}.<template v-if="choice.skipped.some((s) => s.toLowerCase().startsWith('.env'))"> Put the values from .env in the app's Variables tab: it can read a pasted .env file.</template>
      </p>

      <div v-if="detected" class="grid min-w-0 gap-2 rounded-lg border p-3 text-sm">
        <p class="flex items-start gap-2">
          <TriangleAlert v-if="detected.kind === 'none'" class="mt-0.5 size-4 shrink-0 text-destructive" />
          <TriangleAlert v-else-if="detected.kind === 'template' && detected.unsure" class="mt-0.5 size-4 shrink-0 text-warning" />
          <CircleCheck v-else class="mt-0.5 size-4 shrink-0 text-success" />
          <span v-if="detected.kind === 'none'" class="font-medium">{{ detected.label }}</span>
          <span v-else-if="detected.kind === 'template' && detected.unsure" class="font-medium">This could be a website or a server app.</span>
          <span v-else-if="detected.kind === 'template'">
            <template v-if="detected.type === 'static_build'">
              Builds the site and serves the <span class="font-mono font-medium">{{ outputDir || detected.output }}</span> folder.
            </template>
            <template v-else-if="detected.type === 'node'">{{ detected.build ? 'Builds it, then runs it as a Node.js app.' : 'Runs it as a Node.js app.' }}</template>
            <template v-else>Bitploy reads package.json when you deploy and builds the project if it can.</template>
          </span>
          <span v-else-if="detected.kind === 'static' && homeChoices">Serves these files as a website. There is no index.html, so people see the home page first.</span>
          <span v-else-if="detected.kind === 'static' && choice.from === 'page'">Serves this page as a website.</span>
          <span v-else-if="detected.kind === 'static' && detected.home">
            Serves these files as a website, with <span class="font-medium">{{ detected.home }}</span> as the home page.
          </span>
          <span v-else-if="detected.kind === 'static'">Serves these files as a website.</span>
          <span v-else>Builds the Dockerfile at the top of your project.</span>
        </p>
        <p v-if="detected.kind === 'template' && detected.unsure" class="text-xs text-muted-foreground">
          Choose the type below.
        </p>
        <p v-if="detected.kind === 'template' && detected.warn" class="text-xs text-[color-mix(in_oklch,var(--warning)_75%,var(--foreground))]">{{ detected.warn }}</p>
        <div v-if="homeChoices" class="flex min-w-0 items-center gap-2">
          <span class="shrink-0 text-xs text-muted-foreground">Home page</span>
          <Select v-model="homePage">
            <SelectTrigger
              size="sm"
              class="min-w-0 flex-1 *:data-[slot=select-value]:block *:data-[slot=select-value]:truncate sm:max-w-80"
              aria-label="Home page"
            >
              <SelectValue placeholder="Choose a page" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="p in homeChoices" :key="p" :value="p">{{ p }}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <template v-if="detected.kind === 'none'">
          <p class="text-destructive">
            {{ detected.message }} <span class="font-medium">{{ detected.hint }}</span>
          </p>
          <p v-if="detected.assistant" class="text-xs text-muted-foreground">
            Or ask your AI assistant to <RouterLink to="/connect" class="underline underline-offset-2 hover:text-foreground">deploy this folder</RouterLink>.
          </p>
        </template>
      </div>
      <p v-else class="text-xs text-muted-foreground">Bitploy checks what is inside this zip when you deploy.</p>
    </template>
  </div>
</template>
