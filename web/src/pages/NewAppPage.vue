<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { ArrowLeft, FileCode, Github, LayoutGrid, TriangleAlert, Upload } from 'lucide-vue-next'
import PageHeader from '@/components/PageHeader.vue'
import BuildTypeFields from '@/components/BuildTypeFields.vue'
import CatalogPicker from '@/components/CatalogPicker.vue'
import DockerfileEditor from '@/components/DockerfileEditor.vue'
import SourceDropZone from '@/components/SourceDropZone.vue'
import type { UploadChoice } from '@/components/SourceDropZone.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { keys, useMe } from '@/composables/queries'
import { api } from '@/lib/api'
import { detectSource, outputDirError } from '@/lib/detect'
import { friendlyError } from '@/lib/errors'
import type { FriendlyError } from '@/lib/errors'
import { slugify, slugSchema } from '@/lib/slug'
import type { BuildMode, DockerfileFinding, GitSourceInput } from '@/lib/types'
import RepoPicker from './app/RepoPicker.vue'

const router = useRouter()
const route = useRoute()
const qc = useQueryClient()
const me = useMe()

// ---------------------------------------------------------------- name and address
const form = useForm({
  validationSchema: toTypedSchema(
    z.object({
      name: z.string().trim().min(1, 'Give the app a name.').max(80, 'Use at most 80 characters.'),
      slug: slugSchema,
    }),
  ),
  initialValues: { name: '', slug: '' },
})

// The address follows the name until the person edits it.
const editingAddress = ref(false)
const addressEdited = ref(false)
watch(
  () => form.values.name,
  (name) => {
    if (!addressEdited.value) form.setFieldValue('slug', slugify(name ?? ''), !!form.values.slug)
  },
)
function editAddress() {
  editingAddress.value = true
  addressEdited.value = true
}
const addressParts = computed(() => {
  const [before = 'https://', after = '.example.com'] = (me.data.value?.app_url_template ?? 'https://{slug}.example.com').split('{slug}')
  return { before, after }
})

// ---------------------------------------------------------------- source
const github = useQuery({ queryKey: keys.github, queryFn: api.github })
const canPick = computed(() => (github.data.value?.installations ?? []).some((i) => i.status === 'active'))
type Source = 'github' | 'upload' | 'dockerfile' | 'ready-made'
const SOURCES: Source[] = ['github', 'upload', 'dockerfile', 'ready-made']
// ?source= opens a tab directly, e.g. coming back from a ready-made app.
const chosenSource = ref<Source | null>(SOURCES.includes(route.query.source as Source) ? (route.query.source as Source) : null)
// GitHub first, and the default once it is connected.
const source = computed({
  get: () => chosenSource.value ?? (canPick.value ? 'github' : 'upload'),
  set: (v) => (chosenSource.value = v),
})

const repo = ref<GitSourceInput | null>(null)
const upload = ref<UploadChoice | null>(null)
const mode = ref<BuildMode>('auto')
const outputDir = ref('')
// New files, new answer: the folder typed for the old ones does not carry over.
watch(upload, () => (outputDir.value = ''))
const homePage = ref('')
const volume = ref(false)

// The Dockerfile tab: the text is the whole source.
const dockerfile = ref('')
const port = ref('')
const portError = computed(() => {
  const p = String(port.value).trim()
  if (!p) return ''
  const n = Number(p)
  return Number.isInteger(n) && n >= 1 && n <= 65535 ? '' : 'Use a whole number from 1 to 65535.'
})
const findings = ref<DockerfileFinding[]>([])
let checked = ''
async function checkDockerfile() {
  const text = dockerfile.value
  const key = `${text}\n${port.value}`
  if (!text.trim() || key === checked) return
  checked = key
  try {
    const r = await api.validateDockerfile(text, port.value.trim() && !portError.value ? Number(port.value) : undefined)
    if (checked === key) findings.value = r.findings.filter((f) => f.severity !== 'info')
  } catch {
    // The check is a courtesy; deploying says what is wrong.
    findings.value = []
  }
}
watch(dockerfile, () => {
  findings.value = []
  checked = ''
})

// What the server will make of the files; null for GitHub or a zip this page could not read.
const detected = computed(() =>
  source.value === 'upload' && upload.value?.paths
    ? detectSource(upload.value.paths, mode.value, homePage.value, 'Dockerfile', upload.value.packageJson, upload.value.nextConfig)
    : null,
)
// The type is chosen once there is something to build; the files are read later for GitHub and for a zip this page cannot open.
const showType = computed(() => source.value === 'github' || (source.value === 'upload' && !!upload.value))
const typeLater = computed(() => source.value === 'github' || !upload.value?.paths)

const sourceReady = computed(() => {
  if (source.value === 'ready-made') return false
  if (source.value === 'dockerfile') return !!dockerfile.value.trim() && !portError.value
  if (outputDirError(outputDir.value)) return false
  if (source.value === 'github') return !!repo.value
  if (!upload.value) return false
  // Nothing to build: the person has to change something first. A zip this
  // page could not read is checked by the server.
  return detected.value === null || detected.value.kind !== 'none'
})

// What is missing before Deploy works, when it is not said elsewhere already.
const waitingFor = computed(() => {
  if (source.value === 'github') return canPick.value ? 'Pick a repository to continue.' : ''
  if (source.value === 'dockerfile') return dockerfile.value.trim() ? '' : 'Write a Dockerfile to continue.'
  if (source.value === 'ready-made') return ''
  return upload.value ? '' : 'Add your files to continue.'
})

// ---------------------------------------------------------------- submit
const busy = ref(false)
const stage = ref<'creating' | 'sending' | 'checking' | 'starting' | null>(null)
const sent = ref(0)
const error = ref<FriendlyError | null>(null)
const leftover = ref('')
let gone = false
onBeforeUnmount(() => (gone = true))

const buttonLabel = computed(() => {
  switch (stage.value) {
    case 'creating':
      return 'Creating the app...'
    case 'sending':
      return `Sending your files... ${Math.round(sent.value * 100)}%`
    case 'checking':
      return 'Checking your files...'
    case 'starting':
      return 'Starting the deploy...'
    default:
      return 'Deploy'
  }
})

const SLUG_CODES = new Set(['slug_invalid', 'slug_reserved', 'slug_taken'])

async function run(v: { name: string; slug: string }) {
  error.value = null
  leftover.value = ''
  busy.value = true
  stage.value = 'creating'
  let slug = ''
  try {
    const created = await api.createApp({
      name: v.name,
      slug: v.slug,
      volume: volume.value,
      build_mode: source.value === 'dockerfile' ? 'dockerfile' : mode.value,
      port: source.value === 'dockerfile' && port.value.trim() ? Number(port.value) : undefined,
      output_dir: source.value !== 'dockerfile' ? outputDir.value.trim() || undefined : undefined,
      // Only kept when there are several pages to choose from.
      home_page: detected.value?.kind === 'static' && detected.value.choices ? detected.value.home : undefined,
    })
    slug = created.slug
    if (source.value === 'upload' && upload.value) {
      stage.value = 'sending'
      sent.value = 0
      await api.uploadApp(
        slug,
        upload.value.zip,
        (f) => (sent.value = f),
        () => (stage.value = 'checking'),
      )
    } else if (source.value === 'dockerfile') {
      stage.value = 'starting'
      await api.deployFiles(slug, [{ path: 'Dockerfile', content: dockerfile.value }])
    } else if (repo.value) {
      stage.value = 'starting'
      await api.setGitSource(slug, repo.value)
      await api.deployGit(slug)
    }
    void qc.invalidateQueries({ queryKey: keys.apps })
    void qc.invalidateQueries({ queryKey: keys.me })
    void qc.invalidateQueries({ queryKey: keys.deployments(slug) })
    if (!gone) await router.push({ name: 'app', params: { slug } })
  } catch (e) {
    const f = friendlyError(e)
    if (slug) {
      // Do not leave an empty app behind when the first deploy could not start.
      try {
        await api.deleteApp(slug)
      } catch {
        leftover.value = slug
      }
      void qc.invalidateQueries({ queryKey: keys.apps })
    }
    if (SLUG_CODES.has(f.code)) {
      editingAddress.value = true
      addressEdited.value = true
      form.setFieldError('slug', f.hint ? `${f.message} ${f.hint}` : f.message)
    } else {
      error.value = f
    }
  } finally {
    busy.value = false
    stage.value = null
  }
}
const submit = form.handleSubmit(run)
</script>

<template>
  <div class="grid gap-6">
    <RouterLink to="/" class="inline-flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
      <ArrowLeft class="size-4" /> All apps
    </RouterLink>
    <PageHeader title="New app" description="Name it, choose where the code is, and bitploy puts it online." />

    <form novalidate class="grid gap-6" @submit.prevent="submit">
      <Card>
        <CardContent class="grid items-start gap-5 lg:grid-cols-2">
          <FormField v-slot="{ componentField }" name="name">
            <FormItem>
              <FormLabel>Name</FormLabel>
              <FormControl><Input placeholder="My website" autocomplete="off" :disabled="busy" v-bind="componentField" /></FormControl>
              <FormMessage />
            </FormItem>
          </FormField>

          <FormField v-slot="{ componentField }" name="slug">
            <FormItem>
              <div class="flex items-center justify-between gap-3">
                <FormLabel>Address</FormLabel>
                <Button v-if="!editingAddress" type="button" variant="ghost" size="sm" class="-my-1 h-6 px-2 text-xs" :disabled="busy" @click="editAddress">Change</Button>
              </div>
              <div v-if="editingAddress" class="flex min-w-0 items-center gap-2">
                <FormControl><Input autocapitalize="off" autocomplete="off" spellcheck="false" class="min-w-0 font-mono" :disabled="busy" v-bind="componentField" /></FormControl>
                <span class="max-w-[50%] shrink-0 truncate font-mono text-sm text-muted-foreground">{{ addressParts.after }}</span>
              </div>
              <p class="truncate rounded-lg border bg-muted/30 px-3 py-2 font-mono text-[13px] text-muted-foreground" data-testid="address-preview">
                {{ addressParts.before }}<span class="font-medium text-foreground">{{ form.values.slug || 'your-app' }}</span>{{ addressParts.after }}
              </p>
              <FormDescription v-if="!editingAddress">This is where people open your app. It follows the name.</FormDescription>
              <FormMessage />
            </FormItem>
          </FormField>
        </CardContent>
      </Card>

      <Card>
        <CardContent class="grid gap-4">
          <div class="grid gap-1">
            <Label>Where is the code?</Label>
            <Tabs v-model="source" class="min-w-0">
              <TabsList class="max-w-full overflow-x-auto">
                <TabsTrigger value="github" :disabled="busy"><Github class="size-4" /> GitHub</TabsTrigger>
                <TabsTrigger value="upload" :disabled="busy"><Upload class="size-4" /> Upload</TabsTrigger>
                <TabsTrigger value="dockerfile" :disabled="busy"><FileCode class="size-4" /> Dockerfile</TabsTrigger>
                <TabsTrigger value="ready-made" :disabled="busy"><LayoutGrid class="size-4" /> Ready-made</TabsTrigger>
              </TabsList>
            </Tabs>
          </div>

          <RepoPicker v-show="source === 'github'" v-model="repo" compact :show-folder="false" id-prefix="new" />
          <template v-if="source === 'github'">
            <p v-if="repo && mode === 'auto'" class="text-xs text-muted-foreground">Bitploy looks for a Dockerfile in the repository. Without one, it serves an index.html as a website or builds a Node.js project.</p>
          </template>

          <CatalogPicker v-if="source === 'ready-made'" :name="form.values.name" :slug="addressEdited ? form.values.slug : undefined" />

          <SourceDropZone v-if="source === 'upload'" v-model="upload" v-model:mode="mode" v-model:home-page="homePage" :output-dir="outputDir" :disabled="busy" />

          <div v-if="source === 'dockerfile'" class="grid gap-4">
            <div class="grid gap-1.5">
              <Label for="new-dockerfile">Dockerfile</Label>
              <DockerfileEditor
                id="new-dockerfile"
                v-model="dockerfile"
                placeholder="FROM ghcr.io/owner/image:1.2.3"
                :disabled="busy"
                :invalid="findings.some((f) => f.severity === 'error')"
                @blur="checkDockerfile"
              />
              <p class="text-[0.8rem] text-muted-foreground">
                For a published image or a Dockerfile that needs no other files. Without an <span class="font-mono">EXPOSE</span> line, the port is the one the image declares. Files are kept only under <span class="font-mono">/data</span>, with the switch below.
              </p>
              <ul v-if="findings.length" class="grid gap-1.5" data-testid="dockerfile-findings">
                <li
                  v-for="(f, i) in findings"
                  :key="i"
                  :class="['flex items-start gap-2 rounded-lg border px-3 py-2 text-xs', f.severity === 'error' ? 'border-destructive/40 text-destructive' : 'border-warning/40 text-warning']"
                >
                  <TriangleAlert class="mt-0.5 size-3.5 shrink-0" />
                  <span class="min-w-0">
                    <span v-if="f.line" class="font-mono">Line {{ f.line }}: </span>{{ f.message }}
                    <span v-if="f.hint && !/\b[a-z]+(?:_[a-z]+)+\b/.test(f.hint)" class="block text-muted-foreground">{{ f.hint }}</span>
                  </span>
                </li>
              </ul>
            </div>
            <div class="grid max-w-48 gap-1.5">
              <Label for="new-port">Port</Label>
              <Input id="new-port" v-model="port" type="text" inputmode="numeric" autocomplete="off" placeholder="Image default" :disabled="busy" :aria-invalid="!!portError || undefined" @blur="checkDockerfile" />
              <p v-if="portError" class="text-[0.8rem] text-destructive">{{ portError }}</p>
              <p v-else class="text-[0.8rem] text-muted-foreground">Leave empty to use the port the image declares.</p>
            </div>
          </div>

          <BuildTypeFields v-if="showType" v-model:mode="mode" v-model:output-dir="outputDir" :detected="detected" :later="typeLater" :disabled="busy" id-prefix="new" />
        </CardContent>
      </Card>

      <Card v-if="source !== 'ready-made'">
        <CardContent>
          <div class="flex items-center justify-between gap-4">
            <div class="grid gap-1">
              <Label for="new-volume">Keep files in /data</Label>
              <p class="text-[0.8rem] text-muted-foreground">
                Turn this on if your app has a database or saves uploads. Files in <span class="font-mono">/data</span>, like a SQLite database, are kept between deploys. While this is on, each deploy takes the app offline for a few seconds.
              </p>
            </div>
            <Switch id="new-volume" v-model="volume" :disabled="busy" />
          </div>
        </CardContent>
      </Card>

      <Alert v-if="error" variant="destructive">
        <AlertTitle>{{ error.message }}</AlertTitle>
        <AlertDescription>
          <p v-if="error.hint">{{ error.hint }}</p>
          <p v-if="leftover">
            The app <RouterLink :to="{ name: 'app', params: { slug: leftover } }" class="underline">{{ leftover }}</RouterLink> was created but could not be removed again. Delete it in its Settings.
          </p>
        </AlertDescription>
      </Alert>

      <div v-if="source !== 'ready-made'" class="flex flex-wrap items-center gap-3">
        <Button type="submit" :disabled="busy || !sourceReady">{{ buttonLabel }}</Button>
        <Button as-child variant="ghost" :disabled="busy"><RouterLink to="/">Cancel</RouterLink></Button>
        <p v-if="!sourceReady && !busy && waitingFor" class="text-xs text-muted-foreground">{{ waitingFor }}</p>
      </div>
    </form>
  </div>
</template>
