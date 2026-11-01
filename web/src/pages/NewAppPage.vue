<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { ArrowLeft, Github, Upload } from 'lucide-vue-next'
import PageHeader from '@/components/PageHeader.vue'
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
import { detectSource } from '@/lib/detect'
import { friendlyError } from '@/lib/errors'
import type { FriendlyError } from '@/lib/errors'
import { slugify, slugSchema } from '@/lib/slug'
import type { BuildMode, GitSourceInput } from '@/lib/types'
import RepoPicker from './app/RepoPicker.vue'

const router = useRouter()
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
const chosenSource = ref<'github' | 'upload' | null>(null)
// GitHub first, and the default once it is connected.
const source = computed({
  get: () => chosenSource.value ?? (canPick.value ? 'github' : 'upload'),
  set: (v) => (chosenSource.value = v),
})

const repo = ref<GitSourceInput | null>(null)
const upload = ref<UploadChoice | null>(null)
const mode = ref<BuildMode>('auto')
const homePage = ref('')
const volume = ref(false)

// What the server will make of the files; null for GitHub or a zip this page could not read.
const detected = computed(() => (source.value === 'upload' && upload.value?.paths ? detectSource(upload.value.paths, mode.value, homePage.value) : null))

const sourceReady = computed(() => {
  if (source.value === 'github') return !!repo.value
  if (!upload.value) return false
  // Nothing to build: the person has to change something first. A zip this
  // page could not read is checked by the server.
  return detected.value === null || detected.value.kind !== 'none'
})

// What is missing before Deploy works, when it is not said elsewhere already.
const waitingFor = computed(() => {
  if (source.value === 'github') return canPick.value ? 'Pick a repository to continue.' : ''
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
      build_mode: source.value === 'upload' ? mode.value : 'auto',
      // Only kept when there are several pages to choose from.
      home_page: detected.value?.kind === 'static' && detected.value.choices ? detected.value.home : undefined,
      // Plain website files need little memory (the server does the same for a
      // static GitHub repo on its first deploy).
      memory_mb: detected.value?.kind === 'static' ? 128 : undefined,
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
  <div class="mx-auto grid w-full max-w-2xl gap-6">
    <RouterLink to="/" class="inline-flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
      <ArrowLeft class="size-4" /> All apps
    </RouterLink>
    <PageHeader title="New app" description="Name it, choose where the code is, and bitploy puts it online." />

    <form novalidate class="grid gap-6" @submit.prevent="submit">
      <Card>
        <CardContent class="grid gap-5">
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
              <TabsList>
                <TabsTrigger value="github" :disabled="busy"><Github class="size-4" /> GitHub</TabsTrigger>
                <TabsTrigger value="upload" :disabled="busy"><Upload class="size-4" /> Upload</TabsTrigger>
              </TabsList>
            </Tabs>
          </div>

          <RepoPicker v-show="source === 'github'" v-model="repo" compact :show-folder="false" id-prefix="new" />
          <template v-if="source === 'github'">
            <p v-if="repo" class="text-xs text-muted-foreground">Bitploy looks for a Dockerfile in the repository, or an index.html for a plain website.</p>
          </template>

          <SourceDropZone v-if="source === 'upload'" v-model="upload" v-model:mode="mode" v-model:home-page="homePage" :disabled="busy" />
        </CardContent>
      </Card>

      <Card>
        <CardContent>
          <div class="flex items-center justify-between gap-4">
            <div class="grid gap-1">
              <Label for="new-volume">Keep files in /data</Label>
              <p class="text-[0.8rem] text-muted-foreground">
                Files your app saves in <span class="font-mono">/data</span>, like uploads or a database, are kept between deploys. While this is on, each deploy takes the app offline for a few seconds.
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

      <div class="flex flex-wrap items-center gap-3">
        <Button type="submit" :disabled="busy || !sourceReady">{{ buttonLabel }}</Button>
        <Button as-child variant="ghost" :disabled="busy"><RouterLink to="/">Cancel</RouterLink></Button>
        <p v-if="!sourceReady && !busy && waitingFor" class="text-xs text-muted-foreground">{{ waitingFor }}</p>
      </div>
    </form>
  </div>
</template>
