<script setup lang="ts">
import { appPath, appRoute, projectRoute } from '@/lib/appLinks'
import { computed, onBeforeUnmount, ref, watch, watchEffect } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { ArrowRight, Boxes, Container, FileCode, FolderPlus, Github, LayoutGrid, Lock, TriangleAlert, Upload } from 'lucide-vue-next'
import PageHeader from '@/components/PageHeader.vue'
import BuildTypeFields from '@/components/BuildTypeFields.vue'
import CatalogPicker from '@/components/CatalogPicker.vue'
import DomainSelect from '@/components/DomainSelect.vue'
import DockerfileEditor from '@/components/DockerfileEditor.vue'
import SourceDropZone from '@/components/SourceDropZone.vue'
import ComposePreview from '@/components/ComposePreview.vue'
import type { UploadChoice } from '@/components/SourceDropZone.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { keys, useApps, useMe, useProject } from '@/composables/queries'
import { openAdd } from '@/composables/useAddToProject'
import { api } from '@/lib/api'
import { detectSource, outputDirError } from '@/lib/detect'
import { friendlyError } from '@/lib/errors'
import type { FriendlyError } from '@/lib/errors'
import { toastError } from '@/lib/notify'
import { composeFit, NO_OWN_APPS, noOwnApps } from '@/lib/quota'
import { addressParts as toAddressParts, freeSlug, slugify, slugSchema } from '@/lib/slug'
import type { BuildMode, ComposeCheck, DockerfileFinding, GitHubRepo, GitSourceInput } from '@/lib/types'
import RepoPicker from './app/RepoPicker.vue'

const router = useRouter()
const route = useRoute()
const qc = useQueryClient()
const me = useMe()

// ---------------------------------------------------------------- which project
// ?project= adds the app to that project; without it the app gets a project of its own.
const intoProject = computed(() => (typeof route.query.project === 'string' ? route.query.project : ''))
const { list: projects, project } = useProject(intoProject)
const fallback = computed(() => !!projects.data.value?.fallback)
const backTo = computed(() => (project.value ? projectRoute(project.value.id) : '/'))
const thing = computed(() => (intoProject.value ? 'app' : 'project'))
// An account with no apps of its own (Admin > Signups, or its Limits) can only
// add apps to projects shared with it; they count against the owner.
const noOwn = computed(() => noOwnApps(me.data.value) && !project.value?.shared)
const sharedProjects = computed(() => (projects.data.value?.projects ?? []).filter((p) => p.shared))
watchEffect(() => {
  if (project.value) document.title = `Add an app - ${project.value.name} - bitploy`
})

// ---------------------------------------------------------------- name and address
const form = useForm({
  validationSchema: toTypedSchema(
    z.object({
      name: z.string().trim().min(1, 'Give it a name.').max(80, 'Use at most 80 characters.'),
      slug: slugSchema,
    }),
  ),
  initialValues: { name: '', slug: '' },
})

// The address follows the name until the person edits it. An address one of
// your apps has already gets the next free number (portfolio-2); the server
// still decides for addresses of other people.
const editingAddress = ref(false)
const addressEdited = ref(false)
const myApps = useApps()
const takenSlugs = computed(() => new Set((myApps.data.value ?? []).map((a) => a.slug)))
watch(
  [() => form.values.name, takenSlugs],
  ([name]) => {
    if (addressEdited.value || busy.value) return
    const next = freeSlug(slugify(name ?? ''), takenSlugs.value)
    if (next !== form.values.slug) form.setFieldValue('slug', next, !!form.values.slug)
  },
)
/** The app that has the address the name asks for, when the address moved to a free one. */
const slugBumpedFrom = computed(() => {
  const base = slugify(form.values.name ?? '')
  if (addressEdited.value || !base || base === form.values.slug || !takenSlugs.value.has(base)) return null
  return myApps.data.value?.find((a) => a.slug === base) ?? null
})
/** A typed address that one of your apps has: the free one to offer instead. */
const slugClash = computed(() => {
  const s = (form.values.slug ?? '').trim().toLowerCase()
  if (!addressEdited.value || !s || !takenSlugs.value.has(s)) return null
  return { app: myApps.data.value?.find((a) => a.slug === s) ?? null, free: freeSlug(s, takenSlugs.value) }
})
// A project of the same name makes two of them in the sidebar.
const sameNameProject = computed(() => {
  const n = (form.values.name ?? '').trim().toLowerCase()
  if (intoProject.value || fallback.value || !n) return null
  return projects.data.value?.projects.find((p) => p.name.trim().toLowerCase() === n) ?? null
})
function editAddress() {
  editingAddress.value = true
  addressEdited.value = true
}
const domain = ref('') // "" is the default domain
const addressParts = computed(() => toAddressParts(me.data.value, domain.value))

// The name follows the source (the repository, the folder, the image) until the person types one.
let autoName = ''
function suggestName(n: string) {
  const cur = (form.values.name ?? '').trim()
  if (!n || (cur && cur !== autoName)) return
  autoName = n
  form.setFieldValue('name', n, false)
}

// ---------------------------------------------------------------- source
const github = useQuery({ queryKey: keys.github, queryFn: api.github })
const canPick = computed(() => (github.data.value?.installations ?? []).some((i) => i.status === 'active'))
// One "Docker" box takes a pasted Dockerfile (one container) or a compose file
// (several services); the text decides which, so source is 'dockerfile' or 'compose'.
// 'image' runs a published image without a Dockerfile.
type Source = 'github' | 'upload' | 'dockerfile' | 'image' | 'ready-made' | 'compose'
const SOURCES: Source[] = ['github', 'upload', 'dockerfile', 'image', 'ready-made', 'compose']
function fromQuery(q: unknown): Source | null {
  if (q === 'docker') return 'dockerfile'
  return SOURCES.includes(q as Source) ? (q as Source) : null
}
// The step is in the address (?source=): "+ Add" on a project can skip the
// first step, and the browser's Back button returns to the source picker.
const chosenSource = ref<Source | null>(fromQuery(route.query.source))
watch(
  () => route.query.source,
  (q) => (chosenSource.value = fromQuery(q)),
)
function sourceQuery(s: Source | null) {
  const rest = { ...route.query }
  delete rest.source
  return s ? { ...rest, source: s } : rest
}
const source = computed<Source>({
  get: () => chosenSource.value ?? 'github',
  // Dockerfile and compose share one box: switching between them is the same step.
  set: (v) => {
    chosenSource.value = v
    void router.replace({ query: sourceQuery(v) })
  },
})
function pick(s: Source) {
  chosenSource.value = s
  error.value = null
  void router.push({ query: sourceQuery(s) })
}
function pickAnother() {
  chosenSource.value = null
  startRepo.value = null
  void router.push({ query: sourceQuery(null) })
}
// The Docker box's text: a compose file has a top-level services: key, anything else is a Dockerfile.
const pasted = ref('')
const pastedKind = computed<'dockerfile' | 'compose'>(() => (/^services\s*:/m.test(pasted.value) ? 'compose' : 'dockerfile'))
watch(pastedKind, (k) => {
  if (source.value === 'dockerfile' || source.value === 'compose') source.value = k
})

const repo = ref<GitSourceInput | null>(null)
// Picked on the first step: the second opens with it.
const startRepo = ref<GitHubRepo | null>(null)
function startFromRepo(r: GitHubRepo) {
  startRepo.value = r
  pick('github')
}
const upload = ref<UploadChoice | null>(null)
const mode = ref<BuildMode>('auto')
const outputDir = ref('')
// New files, new answer: the folder typed for the old ones does not carry over.
watch(upload, () => (outputDir.value = ''))
const homePage = ref('')
const volume = ref(false)

// After the render: a repository picked on the first step arrives while the name field mounts.
watch(repo, (r) => r && suggestName(r.repo_url.replace(/\.git$/, '').split('/').pop() ?? ''), { flush: 'post' })
watch(upload, (u) => u && suggestName(u.name.replace(/\.(zip|html?)$/i, '')))

// ---------------------------------------------------------------- a published image
const image = ref('')
const imageError = computed(() => {
  const v = image.value.trim()
  if (!v) return ''
  return /^[a-z0-9][a-z0-9._/:@-]*$/i.test(v) ? '' : 'An image name has no spaces, like nginx:1.27 or ghcr.io/owner/app:1.0.'
})
// Private: only the other apps of the project reach it, like a database you bring yourself.
const imagePrivate = ref(false)
watch(image, (v) => {
  const base = v.trim().split('@')[0]!.split('/').pop()?.split(':')[0] ?? ''
  suggestName(base)
})
const privateApp = computed(() => source.value === 'image' && imagePrivate.value)

// ---------------------------------------------------------------- several services (a compose file)
const composeText = pasted
const PASTE_PLACEHOLDER = 'FROM ghcr.io/owner/image:1.2.3\n\n# or a compose file:\nservices:\n  web:\n    image: ghcr.io/owner/app:1.0\n    ports: ["3000"]\n    depends_on: [postgres]\n  postgres:\n    image: postgres:16'
const composeCheck = ref<ComposeCheck | null | undefined>(undefined)
// An upload with a compose file at the top runs all its services, unless the person picks one container.
const uploadCompose = computed(() => (source.value === 'upload' ? (upload.value?.compose ?? null) : null))
const useUploadCompose = ref(true)
watch(upload, () => (useUploadCompose.value = true))
// Only GitHub offers the compose type; other sources pick their own.
watch(source, (v) => v !== 'github' && mode.value === 'compose' && (mode.value = 'auto'))
/** Deploys as several services: a pasted compose file, an upload with one, or GitHub with the compose type. */
const composeMode = computed(
  () => source.value === 'compose' || (!!uploadCompose.value && useUploadCompose.value) || (source.value === 'github' && mode.value === 'compose'),
)
// The text the preview reads: pasted or found in the upload. GitHub repositories are read when they build.
const previewText = computed(() => (source.value === 'compose' ? composeText.value : uploadCompose.value && useUploadCompose.value ? uploadCompose.value.text : ''))
const previewFrom = computed(() => (source.value === 'compose' ? '' : uploadCompose.value ? uploadCompose.value.path : ''))
// Known problems keep Deploy off; a server without a preview leaves it to the deploy.
// Over the allowance is one of them, by the rule the Add dialog uses (lib/quota.ts).
const composeOver = computed(() => !!composeCheck.value && !composeFit(composeCheck.value, me.data.value).fits)
// A pasted file comes without code, so a service with build: cannot be built.
const composeNeedsImage = computed(() => source.value === 'compose' && (composeCheck.value?.services ?? []).some((s) => s.kind === 'build'))
const composeBlocked = computed(
  () => !!previewText.value && (composeCheck.value === undefined || !!composeCheck.value?.errors?.length || composeOver.value || composeNeedsImage.value),
)
const composeWaiting = computed(() => {
  if (composeCheck.value === undefined) return 'Checking the compose file...'
  if (composeCheck.value?.errors?.length) return ''
  if (composeNeedsImage.value) return 'Give every service an image: line to continue.'
  if (composeOver.value) return 'It does not fit in what is left of your allowance.'
  return ''
})
const previewAddress = computed(() => `${addressParts.value.before}${form.values.slug || 'your-app'}${addressParts.value.after}`.replace(/^https?:\/\//, ''))

// A pasted Dockerfile: the text is the whole source.
const dockerfile = pasted
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
  if (source.value !== 'dockerfile') return
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
const showType = computed(() => source.value === 'github' || (source.value === 'upload' && !!upload.value && !(uploadCompose.value && useUploadCompose.value)))
const typeLater = computed(() => source.value === 'github' || !upload.value?.paths)

const sourceReady = computed(() => {
  if (source.value === 'ready-made') return false
  if (source.value === 'compose') return !!composeText.value.trim() && !composeBlocked.value
  if (source.value === 'dockerfile') return !!dockerfile.value.trim() && !portError.value
  if (source.value === 'image') return !!image.value.trim() && !imageError.value && !portError.value && (!imagePrivate.value || !!port.value.trim())
  if (outputDirError(outputDir.value)) return false
  if (source.value === 'github') return !!repo.value
  if (!upload.value) return false
  if (uploadCompose.value && useUploadCompose.value) return !composeBlocked.value
  // Nothing to build: the person has to change something first. A zip this
  // page could not read is checked by the server.
  return detected.value === null || detected.value.kind !== 'none'
})

// What is missing before Deploy works, when it is not said elsewhere already.
const waitingFor = computed(() => {
  if (source.value === 'github') return canPick.value ? 'Pick a repository to continue.' : ''
  if (source.value === 'dockerfile') return dockerfile.value.trim() ? '' : 'Paste a Dockerfile or a compose file to continue.'
  if (source.value === 'image') return !image.value.trim() ? 'Type the image name to continue.' : imagePrivate.value && !port.value.trim() ? 'Type the port the other apps connect to.' : ''
  if (source.value === 'ready-made') return ''
  if (source.value === 'compose') return composeText.value.trim() ? (composeBlocked.value ? composeWaiting.value || 'Fix the problems above to continue.' : '') : 'Paste a compose file to continue.'
  if (uploadCompose.value && useUploadCompose.value && composeBlocked.value) return composeWaiting.value || 'Fix the compose file, or run one container instead.'
  return upload.value ? '' : 'Add your files to continue.'
})

// ---------------------------------------------------------------- the first step: where is the code?
interface Tile {
  id: Source
  title: string
  text: string
  icon: typeof Github
}
const TILES: Tile[] = [
  { id: 'upload', title: 'Upload', text: 'A folder, a .zip or one .html page from your computer.', icon: Upload },
  { id: 'dockerfile', title: 'Dockerfile or compose', text: 'Paste a Dockerfile, or a compose file for several services.', icon: FileCode },
  { id: 'image', title: 'Docker image', text: 'A published image, like nginx:1.27 or ghcr.io/owner/app.', icon: Container },
  { id: 'ready-made', title: 'Ready-made', text: 'Uptime Kuma, Vaultwarden, PocketBase and more.', icon: LayoutGrid },
]
const SOURCE_LABEL: Record<Source, string> = {
  github: 'From GitHub',
  upload: 'Upload',
  dockerfile: 'Dockerfile or compose file',
  compose: 'Dockerfile or compose file',
  image: 'Docker image',
  'ready-made': 'Ready-made',
}

// "Start empty": a project with nothing in it yet, then the Add menu.
const emptyOpen = ref(false)
const emptyName = ref('')
const emptyBusy = ref(false)
async function startEmpty() {
  const name = emptyName.value.trim()
  if (!name) return
  emptyBusy.value = true
  try {
    const p = await api.createProject({ name })
    void qc.invalidateQueries({ queryKey: keys.projects })
    await router.push(projectRoute(p.id))
    openAdd(p.id)
  } catch (e) {
    toastError(e)
  } finally {
    emptyBusy.value = false
  }
}

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
      return intoProject.value ? 'Adding the app...' : 'Creating the project...'
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
  const pasteDeploy = source.value === 'dockerfile' || source.value === 'image'
  try {
    const created = await api.createApp({
      name: v.name,
      slug: v.slug,
      project: intoProject.value || undefined,
      // A private image has no address: the other apps of the project reach it on its port.
      type: privateApp.value ? 'worker' : undefined,
      domain: privateApp.value ? undefined : domain.value || undefined,
      // A compose file brings its own volumes.
      volume: composeMode.value ? false : volume.value,
      build_mode: composeMode.value ? 'compose' : pasteDeploy ? 'dockerfile' : mode.value,
      port: pasteDeploy && port.value.trim() ? Number(port.value) : undefined,
      output_dir: !pasteDeploy && !composeMode.value ? outputDir.value.trim() || undefined : undefined,
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
    } else if (source.value === 'image') {
      // A one-line Dockerfile runs the image as it is.
      stage.value = 'starting'
      await api.deployFiles(slug, [{ path: 'Dockerfile', content: `FROM ${image.value.trim()}\n` }])
    } else if (source.value === 'compose') {
      // Sent like a pasted Dockerfile: the file is the whole source.
      stage.value = 'starting'
      await api.deployFiles(slug, [{ path: 'compose.yaml', content: composeText.value }])
    } else if (repo.value) {
      stage.value = 'starting'
      await api.setGitSource(slug, repo.value)
      await api.deployGit(slug)
    }
    void qc.invalidateQueries({ queryKey: keys.apps })
    void qc.invalidateQueries({ queryKey: keys.me })
    void qc.invalidateQueries({ queryKey: keys.deployments(slug) })
    if (!gone) await router.push(appRoute(created))
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
    <PageHeader
      :title="project ? `Add an app to ${project.name}` : 'New project'"
      :description="noOwn ? undefined : chosenSource ? `Name the ${thing} and press Deploy.` : 'Where is your code? bitploy builds it and puts it online.'"
    />

    <Alert v-if="noOwn" data-testid="no-own-apps">
      <AlertTitle>{{ NO_OWN_APPS }}</AlertTitle>
      <AlertDescription>
        <template v-if="sharedProjects.length">
          <p>You can add apps to the projects shared with you:</p>
          <ul class="mt-1 grid gap-1">
            <li v-for="p in sharedProjects" :key="p.id">
              <RouterLink :to="{ name: 'new-app', query: { project: p.id } }" class="text-foreground underline underline-offset-4">Add an app to {{ p.name }}</RouterLink>
            </li>
          </ul>
        </template>
      </AlertDescription>
    </Alert>

    <!-- Step 1: where the code is. -->
    <div v-else-if="!chosenSource" class="grid gap-3" data-testid="source-picker">
      <!-- GitHub connected: the repositories, right here. -->
      <Card v-if="canPick" data-testid="start-github">
        <CardContent class="grid gap-4">
          <div class="flex items-start gap-3">
            <span class="flex size-10 shrink-0 items-center justify-center rounded-lg border bg-background"><Github class="size-5" /></span>
            <span class="grid gap-0.5">
              <span class="flex items-center gap-2 text-base font-semibold">
                Deploy from GitHub
                <span class="inline-flex items-center gap-1.5 rounded bg-success/10 px-1.5 py-0.5 text-[11px] font-medium text-success"><span class="size-1.5 rounded-full bg-success" aria-hidden="true" />Connected</span>
              </span>
              <span class="text-sm text-muted-foreground">Pick a repository. bitploy finds a Dockerfile, a compose file, a website or a Node.js app, and deploys again on every push.</span>
            </span>
          </div>
          <RepoPicker compact :show-folder="false" id-prefix="start" @picked="startFromRepo" />
        </CardContent>
      </Card>
      <button
        v-else
        type="button"
        class="group relative grid gap-3 overflow-hidden rounded-xl border bg-card p-5 text-left transition-colors hover:border-primary/50 sm:grid-cols-[auto_1fr_auto] sm:items-center"
        @click="pick('github')"
      >
        <span class="flex size-11 items-center justify-center rounded-lg border bg-background"><Github class="size-5" /></span>
        <span class="grid gap-1">
          <span class="text-base font-semibold">Deploy from GitHub</span>
          <span class="text-sm text-muted-foreground">
            Any repository. bitploy finds a Dockerfile, a compose file, a website or a Node.js app, and deploys again on every push.
          </span>
          <span class="pt-1 text-xs text-muted-foreground">Connect GitHub on the next step</span>
        </span>
        <ArrowRight class="hidden size-4 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:text-foreground sm:block" />
      </button>

      <h2 class="pt-3 text-sm font-medium text-muted-foreground">Or start from</h2>
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <button
          v-for="t in TILES"
          :key="t.id"
          type="button"
          class="group grid content-start gap-2 rounded-xl border bg-card p-4 text-left transition-colors hover:border-foreground/25"
          @click="pick(t.id)"
        >
          <component :is="t.icon" class="size-4.5 text-muted-foreground group-hover:text-foreground" />
          <span class="text-sm font-medium">{{ t.title }}</span>
          <span class="text-xs text-muted-foreground">{{ t.text }}</span>
        </button>
      </div>

      <div v-if="!intoProject && !fallback && projects.data.value" class="rounded-xl border border-dashed p-4">
        <div v-if="!emptyOpen" class="flex flex-wrap items-center justify-between gap-3">
          <span class="flex items-center gap-3">
            <FolderPlus class="size-4.5 text-muted-foreground" />
            <span class="grid">
              <span class="text-sm font-medium">Empty project</span>
              <span class="text-xs text-muted-foreground">Add a database, a cache or an app to it after creating it.</span>
            </span>
          </span>
          <Button variant="outline" size="sm" @click="emptyOpen = true">Create empty project</Button>
        </div>
        <form v-else class="flex flex-wrap items-end gap-2" @submit.prevent="startEmpty">
          <div class="grid gap-1.5">
            <Label for="empty-name">Project name</Label>
            <Input id="empty-name" v-model="emptyName" class="w-64" placeholder="Shop" autocomplete="off" :disabled="emptyBusy" />
          </div>
          <Button type="submit" :disabled="!emptyName.trim() || emptyBusy">{{ emptyBusy ? 'Creating...' : 'Create project' }}</Button>
          <Button type="button" variant="ghost" :disabled="emptyBusy" @click="emptyOpen = false">Cancel</Button>
        </form>
      </div>
    </div>

    <!-- Step 2: the code, the name, Deploy. -->
    <form v-else novalidate class="grid gap-6" @submit.prevent="submit">
      <Card>
        <CardContent class="grid gap-4">
          <div class="flex items-center justify-between gap-3">
            <Label class="text-sm">{{ SOURCE_LABEL[source] }}</Label>
            <Button type="button" variant="ghost" size="sm" class="-my-1 h-7 px-2 text-xs" :disabled="busy" @click="pickAnother">Pick another source</Button>
          </div>

          <RepoPicker v-show="source === 'github'" v-model="repo" compact :show-folder="false" id-prefix="new" :start="startRepo" />
          <template v-if="source === 'github'">
            <p v-if="repo && mode === 'auto'" class="text-xs text-muted-foreground">bitploy looks for a Dockerfile in the repository. Without one, it serves an index.html as a website or builds a Node.js app.</p>
          </template>

          <CatalogPicker v-if="source === 'ready-made'" :name="form.values.name" :slug="addressEdited ? form.values.slug : undefined" :domain="domain" :project="intoProject || undefined" />

          <SourceDropZone v-if="source === 'upload'" v-model="upload" v-model:mode="mode" v-model:home-page="homePage" :output-dir="outputDir" :compose-chosen="!!uploadCompose && useUploadCompose" :disabled="busy" />

          <!-- The upload has a compose file: run all its services, or just one container. -->
          <div v-if="uploadCompose" class="grid gap-3">
            <div class="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label="What to run">
              <button
                v-for="o in [
                  { value: true, title: 'All services in the compose file', text: `Runs every service of ${uploadCompose.path}, each in its own container.` },
                  { value: false, title: 'One container', text: 'Ignores the compose file and builds one container from the Dockerfile.' },
                ]"
                :key="String(o.value)"
                type="button"
                role="radio"
                :aria-checked="useUploadCompose === o.value"
                :disabled="busy"
                class="grid gap-0.5 rounded-lg border p-3 text-left transition-colors hover:bg-accent/50 aria-checked:border-primary/50 aria-checked:bg-primary/5"
                @click="useUploadCompose = o.value"
              >
                <span class="flex items-center gap-2 text-sm font-medium">
                  <span class="flex size-3.5 items-center justify-center rounded-full border" :class="useUploadCompose === o.value && 'border-primary'" aria-hidden="true">
                    <span v-if="useUploadCompose === o.value" class="size-1.5 rounded-full bg-primary" />
                  </span>
                  {{ o.title }}
                </span>
                <span class="pl-5.5 text-xs text-muted-foreground">{{ o.text }}</span>
              </button>
            </div>
            <ComposePreview v-if="useUploadCompose" v-model:check="composeCheck" :text="uploadCompose.text" :from="previewFrom" :address="previewAddress" />
          </div>

          <div v-if="source === 'dockerfile' || source === 'compose'" class="grid gap-4">
            <div class="grid gap-1.5">
              <div class="flex items-center justify-between gap-3">
                <Label for="new-docker">Paste it here</Label>
                <span v-if="pasted.trim()" class="inline-flex items-center gap-1.5 text-xs text-muted-foreground" data-testid="pasted-kind">
                  <component :is="source === 'compose' ? Boxes : FileCode" class="size-3.5" />
                  {{ source === 'compose' ? 'Compose file: several services' : 'Dockerfile: one container' }}
                </span>
              </div>
              <DockerfileEditor
                id="new-docker"
                v-model="pasted"
                :rows="12"
                :placeholder="PASTE_PLACEHOLDER"
                :disabled="busy"
                :invalid="source === 'dockerfile' && findings.some((f) => f.severity === 'error')"
                @blur="checkDockerfile"
              />
              <p v-if="!pasted.trim()" class="text-[0.8rem] text-muted-foreground">
                A Dockerfile runs one container. A compose file runs several services, like an app with its database.
              </p>
              <p v-else-if="source === 'compose'" class="text-[0.8rem] text-muted-foreground">
                Every service needs a published image, like <span class="font-mono">postgres</span> or <span class="font-mono">redis</span>. If a service builds your own code, upload the folder or pick the GitHub repository instead.
              </p>
              <p v-else class="text-[0.8rem] text-muted-foreground">
                Only for a Dockerfile that needs no other files. Without an <span class="font-mono">EXPOSE</span> line, bitploy uses the port the image declares. Files are kept between deploys only in <span class="font-mono">/data</span>, with Keep saved files turned on below.
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
            <ComposePreview v-if="source === 'compose' && composeText.trim()" v-model:check="composeCheck" :text="composeText" :address="previewAddress" pasted />
          </div>

          <div v-if="source === 'image'" class="grid gap-4">
            <div class="grid gap-1.5">
              <Label for="new-image">Image</Label>
              <Input id="new-image" v-model="image" class="font-mono" placeholder="ghcr.io/owner/app:1.0" autocapitalize="off" autocomplete="off" spellcheck="false" :disabled="busy" :aria-invalid="!!imageError || undefined" />
              <p v-if="imageError" class="text-[0.8rem] text-destructive">{{ imageError }}</p>
              <p v-else class="text-[0.8rem] text-muted-foreground">From Docker Hub, GitHub (ghcr.io) or another public registry. Add a version after the colon, like :1.27, to keep that version.</p>
            </div>
            <div v-if="intoProject" class="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label="Who reaches it">
              <button
                v-for="o in [
                  { value: false, title: 'Visitors, at a web address', text: 'A website or an API people open.' },
                  { value: true, title: `Only the apps of ${project?.name ?? 'the project'}`, text: 'No web address, like a database you bring yourself.' },
                ]"
                :key="String(o.value)"
                type="button"
                role="radio"
                :aria-checked="imagePrivate === o.value"
                :disabled="busy"
                class="grid gap-0.5 rounded-lg border p-3 text-left transition-colors hover:bg-accent/50 aria-checked:border-primary/50 aria-checked:bg-primary/5"
                @click="imagePrivate = o.value"
              >
                <span class="flex items-center gap-2 text-sm font-medium">
                  <span class="flex size-3.5 items-center justify-center rounded-full border" :class="imagePrivate === o.value && 'border-primary'" aria-hidden="true">
                    <span v-if="imagePrivate === o.value" class="size-1.5 rounded-full bg-primary" />
                  </span>
                  <Lock v-if="o.value" class="size-3.5 text-muted-foreground" />
                  {{ o.title }}
                </span>
                <span class="pl-5.5 text-xs text-muted-foreground">{{ o.text }}</span>
              </button>
            </div>
          </div>

          <div v-if="source === 'dockerfile' || source === 'image'" class="grid max-w-56 gap-1.5">
            <Label for="new-port">Port</Label>
            <Input id="new-port" v-model="port" type="text" inputmode="numeric" autocomplete="off" :placeholder="privateApp ? '5432' : 'Image default'" :disabled="busy" :aria-invalid="!!portError || undefined" @blur="checkDockerfile" />
            <p v-if="portError" class="text-[0.8rem] text-destructive">{{ portError }}</p>
            <p v-else-if="privateApp" class="text-[0.8rem] text-muted-foreground">The port the other apps connect to.</p>
            <p v-else class="text-[0.8rem] text-muted-foreground">Leave empty to use the port the image declares.</p>
          </div>

          <BuildTypeFields v-if="showType" v-model:mode="mode" v-model:output-dir="outputDir" :detected="detected" :later="typeLater" :disabled="busy" :compose="source === 'github'" id-prefix="new" />
        </CardContent>
      </Card>

      <Card v-if="source !== 'ready-made'">
        <CardContent class="grid items-start gap-5 lg:grid-cols-2">
          <FormField v-slot="{ componentField }" name="name">
            <FormItem>
              <FormLabel>Name</FormLabel>
              <FormControl><Input :placeholder="intoProject ? 'api' : 'My website'" autocomplete="off" :disabled="busy" v-bind="componentField" /></FormControl>
              <FormDescription v-if="intoProject">Also its short name in {{ project?.name ?? 'the project' }}: the other apps reach it at this name.</FormDescription>
              <p v-else-if="sameNameProject" class="text-[0.8rem] text-[color-mix(in_oklch,var(--warning)_75%,var(--foreground))]" data-testid="same-name-project">
                You already have a project called {{ sameNameProject.name }}. Deploying makes a second one; give this one another name to tell them apart.
              </p>
              <FormMessage />
            </FormItem>
          </FormField>

          <FormField v-slot="{ componentField }" name="slug">
            <FormItem>
              <div class="flex items-center justify-between gap-3">
                <FormLabel>{{ privateApp ? 'ID' : 'Address' }}</FormLabel>
                <Button v-if="!editingAddress" type="button" variant="ghost" size="sm" class="-my-1 h-6 px-2 text-xs" :disabled="busy" @click="editAddress">Change</Button>
              </div>
              <div v-if="editingAddress" class="flex min-w-0 items-center gap-2">
                <FormControl><Input autocapitalize="off" autocomplete="off" spellcheck="false" class="min-w-0 font-mono" :disabled="busy" v-bind="componentField" /></FormControl>
                <DomainSelect v-if="!privateApp" v-model="domain" :disabled="busy" />
              </div>
              <p v-if="privateApp" class="truncate rounded-lg border bg-muted/30 px-3 py-2 font-mono text-[13px] text-muted-foreground">
                <Lock class="mr-1 inline size-3" /> Private, at <span class="font-medium text-foreground">{{ slugify(form.values.name ?? '') || 'name' }}</span>{{ port.trim() ? `:${port.trim()}` : '' }}
              </p>
              <p v-else class="truncate rounded-lg border bg-muted/30 px-3 py-2 font-mono text-[13px] text-muted-foreground" data-testid="address-preview">
                {{ addressParts.before }}<span class="font-medium text-foreground">{{ form.values.slug || 'your-app' }}</span>{{ addressParts.after }}
              </p>
              <p v-if="slugClash" class="text-[0.8rem] text-destructive" data-testid="slug-clash">
                Your app {{ slugClash.app?.name ?? form.values.slug }} has this {{ privateApp ? 'ID' : 'address' }}.
                <button v-if="slugClash.free" type="button" class="font-medium underline underline-offset-2" :disabled="busy" @click="form.setFieldValue('slug', slugClash.free)">
                  Use {{ slugClash.free }}
                </button>
              </p>
              <FormDescription v-else-if="slugBumpedFrom" data-testid="slug-bumped">
                Your app {{ slugBumpedFrom.name }} has {{ slugBumpedFrom.slug }}, so this one gets {{ form.values.slug }}.
              </FormDescription>
              <FormDescription v-else-if="privateApp">No web address. The ID is used in the dashboard's links to it.</FormDescription>
              <FormDescription v-else-if="!editingAddress">Where people open your app. It follows the name until you change it.</FormDescription>
              <FormMessage />
            </FormItem>
          </FormField>
        </CardContent>
      </Card>

      <Card v-if="source !== 'ready-made' && !composeMode">
        <CardContent>
          <div class="flex items-center justify-between gap-4">
            <div class="grid gap-1">
              <Label for="new-volume">Keep saved files <span class="font-mono text-xs font-normal text-muted-foreground">/data</span></Label>
              <p class="text-[0.8rem] text-muted-foreground">
                For an app with a database or uploads. Files in <span class="font-mono">/data</span>, like a SQLite database, are kept between deploys. Each deploy then takes the app offline for a few seconds.
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
            The app <RouterLink :to="appPath(leftover)" class="underline">{{ leftover }}</RouterLink> was created but could not be removed again. Delete it in its Settings.
          </p>
        </AlertDescription>
      </Alert>

      <div v-if="source !== 'ready-made'" class="flex flex-wrap items-center gap-3">
        <Button type="submit" :disabled="busy || !sourceReady || !!slugClash">{{ buttonLabel }}</Button>
        <Button as-child variant="ghost" :disabled="busy"><RouterLink :to="backTo">Cancel</RouterLink></Button>
        <p v-if="!sourceReady && !busy && waitingFor" class="text-xs text-muted-foreground">{{ waitingFor }}</p>
      </div>
    </form>
  </div>
</template>
