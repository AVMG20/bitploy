<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import FormScope from '@/components/FormScope.vue'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { KeyRound, Pencil, Trash2 } from 'lucide-vue-next'
import DockerfileEditor from '@/components/DockerfileEditor.vue'
import BuildTypeFields from '@/components/BuildTypeFields.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import GitConnect from './GitConnect.vue'
import GitLinked from './GitLinked.vue'
import PasswordProtection from './PasswordProtection.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { REFRESH_MS, keys, livePoll, useApp, useCatalogApp, useMe } from '@/composables/queries'
import { api } from '@/lib/api'
import { outputDirError } from '@/lib/detect'
import { formatMB } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import { queryClient } from '@/lib/queryClient'
import type { BuildMode, UpdateAppInput } from '@/lib/types'

const props = defineProps<{ slug: string }>()
const router = useRouter()
const qc = useQueryClient()
const app = useApp(() => props.slug)
const me = useMe()
const quota = computed(() => me.data.value?.quota)

// ---------------------------------------------------------------- name and address
const generalForm = useForm({
  validationSchema: toTypedSchema(
    z.object({
      name: z.string().trim().min(1, 'Give the app a name.').max(80, 'Use at most 80 characters.'),
      slug: z
        .string()
        .trim()
        .toLowerCase()
        .min(3, 'Use at least 3 characters.')
        .max(40, 'Use at most 40 characters.')
        .regex(/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/, 'Use letters, numbers and -, not at the start or end.'),
    }),
  ),
  initialValues: { name: '', slug: '' },
})

let generalLoadedFor = ''
watch(
  () => app.data.value,
  (a) => {
    if (a && generalLoadedFor !== a.id) {
      generalLoadedFor = a.id
      generalForm.resetForm({ values: { name: a.name, slug: a.slug } })
    }
  },
  { immediate: true },
)

// A new subdomain moves the app, so it is confirmed first.
const pendingRename = ref<{ name: string; slug: string } | null>(null)
const renameOpen = computed({
  get: () => pendingRename.value !== null,
  set: (v) => {
    if (!v) pendingRename.value = null
  },
})

const saveGeneral = useMutation({
  mutationFn: (v: { name: string; slug: string }) => {
    const input: UpdateAppInput = {}
    if (v.name !== app.data.value?.name) input.name = v.name
    if (v.slug !== props.slug) input.slug = v.slug
    return api.updateApp(props.slug, input)
  },
  onSuccess: async (updated) => {
    const oldSlug = props.slug
    pendingRename.value = null
    generalForm.resetForm({ values: { name: updated.name, slug: updated.slug } })
    void qc.invalidateQueries({ queryKey: keys.apps })
    if (updated.slug === oldSlug) {
      toastOk('Name saved')
      void qc.invalidateQueries({ queryKey: keys.app(oldSlug) })
      return
    }
    toastOk('Address changed', updated.url ? `The app now lives at ${updated.url}. The old address no longer works.` : undefined)
    qc.setQueryData(keys.app(updated.slug), updated)
    await router.replace({ name: 'app-settings', params: { slug: updated.slug } })
    for (const key of [keys.app(oldSlug), keys.deployments(oldSlug), keys.env(oldSlug), keys.domains(oldSlug)]) {
      queryClient.removeQueries({ queryKey: key })
    }
    queryClient.removeQueries({ queryKey: ['metrics', oldSlug] })
  },
  onError: (e) => {
    pendingRename.value = null
    toastError(e)
  },
})
const submitGeneral = generalForm.handleSubmit((v) => {
  if (v.slug !== props.slug) pendingRename.value = v
  else if (v.name !== app.data.value?.name) saveGeneral.mutate(v)
})
const renameDescription = computed(() => {
  const to = `${pendingRename.value?.slug ?? ''}.${me.data.value?.apps_domain ?? ''}`
  return `The app moves to ${to} and is down for a few seconds. The old address stops working, so links and bookmarks to it break. Files in /data are kept.`
})

// ---------------------------------------------------------------- env vars
const env = useQuery({
  queryKey: computed(() => keys.env(props.slug)),
  queryFn: () => api.env(props.slug),
})

const envForm = useForm({
  validationSchema: toTypedSchema(
    z.object({
      name: z
        .string()
        .trim()
        .min(1, 'Give the variable a name.')
        .regex(/^[A-Za-z_][A-Za-z0-9_]*$/, 'Use letters, numbers and underscores only, like API_KEY.'),
      value: z.string(),
    }),
  ),
  initialValues: { name: '', value: '' },
})

const saveEnv = useMutation({
  mutationFn: (v: { name: string; value: string }) => api.setEnv(props.slug, { [v.name]: v.value }, []),
  onSuccess: (_r, v) => {
    toastOk(`Saved ${v.name}`, 'Restart the app to use the new value.')
    envForm.resetForm({ values: { name: '', value: '' } })
    void qc.invalidateQueries({ queryKey: keys.env(props.slug) })
  },
  onError: (e) => toastError(e),
})
const submitEnv = envForm.handleSubmit((v) => saveEnv.mutate({ name: v.name.trim(), value: v.value }))

async function editEnv(name: string) {
  envForm.resetForm({ values: { name, value: '' } })
  await nextTick()
  document.querySelector<HTMLElement>('#env-form textarea')?.focus()
}

const envToRemove = ref<string | null>(null)
const envRemoveOpen = computed({
  get: () => envToRemove.value !== null,
  set: (v) => {
    if (!v) envToRemove.value = null
  },
})
const removeEnv = useMutation({
  mutationFn: (name: string) => api.setEnv(props.slug, {}, [name]),
  onSuccess: (_r, name) => {
    toastOk(`Removed ${name}`)
    envToRemove.value = null
    void qc.invalidateQueries({ queryKey: keys.env(props.slug) })
  },
  onError: (e) => toastError(e),
})

// ---------------------------------------------------------------- type
// How the files become an app. Read by the next deploy, not by the running app.
const typeMode = ref<BuildMode>('auto')
const typeOutput = ref('')
let typeLoadedFor = ''
watch(
  () => app.data.value,
  (a) => {
    // Once per app, so a poll does not wipe unsaved edits.
    if (a && typeLoadedFor !== a.id) {
      typeLoadedFor = a.id
      typeMode.value = a.build_mode
      typeOutput.value = a.output_dir
    }
  },
  { immediate: true },
)
const typeChanged = computed(() => !!app.data.value && (typeMode.value !== app.data.value.build_mode || typeOutput.value.trim() !== app.data.value.output_dir))
const saveType = useMutation({
  mutationFn: () => api.updateApp(props.slug, { build_mode: typeMode.value, output_dir: typeOutput.value.trim() }),
  onSuccess: (updated) => {
    toastOk('Type saved', 'It applies on the next deploy.')
    typeOutput.value = updated.output_dir
    void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.apps })
  },
  onError: (e) => toastError(e),
})

// ---------------------------------------------------------------- resources
// What this app may have: the quota minus what the user's other running apps
// already have (same rule as the server when the app starts).
const room = computed(() => {
  const q = quota.value
  const u = me.data.value?.usage
  const a = app.data.value
  if (!q || !u || !a) return null
  // A static site is not part of the reserved totals, so there is nothing to take off.
  const runs = a.desired_state === 'running' && !!a.current_deployment && !a.static
  const otherMem = Math.max(0, u.memory_reserved_mb - (runs ? a.memory_mb : 0))
  const otherCPU = Math.max(0, u.cpu_reserved_millis - (runs ? a.cpu_millis : 0))
  return {
    otherMem,
    otherCPU,
    mem: Math.max(0, q.memory_mb - otherMem),
    cpus: Math.max(0, q.cpu_millis - otherCPU) / 1000,
  }
})

const isStatic = computed(() => !!app.data.value?.static)

const resForm = useForm({
  validationSchema: computed(() =>
    toTypedSchema(
      z.object({
        memory_mb: z.coerce
          .number({ invalid_type_error: 'Enter a number.' })
          .int('Use a whole number.')
          .min(64, 'At least 64 MB.')
          .max(room.value?.mem ?? 100000, `You have ${room.value?.mem ?? ''} MB left for this app.`),
        cpus: z.coerce
          .number({ invalid_type_error: 'Enter a number.' })
          .min(0.1, 'At least 0.1.')
          .max(room.value?.cpus ?? 100, `You have ${room.value?.cpus ?? ''} CPUs left for this app.`),
      }),
    ),
  ),
  initialValues: { memory_mb: 512, cpus: 0.5 },
})

let resLoadedFor = ''
watch(
  () => app.data.value,
  (a) => {
    // Fill the form once per app (not on every poll, which would wipe unsaved edits).
    // Again when the app turns into a static site or back: the size changes with it.
    const key = a ? `${a.id}:${a.static}` : ''
    if (a && resLoadedFor !== key) {
      resLoadedFor = key
      resForm.resetForm({ values: { memory_mb: a.memory_mb, cpus: a.cpu_millis / 1000 } })
    }
  },
  { immediate: true },
)

const saveRes = useMutation({
  mutationFn: (v: { memory_mb: number; cpus: number }) =>
    api.updateApp(props.slug, { memory_mb: v.memory_mb, cpu_millis: Math.round(v.cpus * 1000) }),
  onSuccess: () => {
    toastOk('Size saved', 'Restart the app to apply it.')
    void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.me })
  },
  onError: (e) => toastError(e),
})
const submitRes = resForm.handleSubmit((v) => saveRes.mutate(v))

// ---------------------------------------------------------------- dockerfile
const dockerfile = useQuery({
  queryKey: computed(() => keys.dockerfile(props.slug)),
  queryFn: () => api.dockerfile(props.slug),
  retry: false,
})
const dockerfileText = ref('')
// What the editor was loaded from. A refetch (a deploy finished, someone saved
// elsewhere) replaces the text only while there are no local edits.
const dockerfileBase = ref<string | null>(null)
watch(
  () => [props.slug, dockerfile.data.value] as const,
  ([, d], old) => {
    if (!d) return
    const otherApp = !!old && old[0] !== props.slug
    if (otherApp || dockerfileBase.value === null || dockerfileText.value === dockerfileBase.value) {
      dockerfileText.value = d.content
      dockerfileBase.value = d.content
    }
  },
  { immediate: true },
)
const dockerfileDirty = computed(() => dockerfileBase.value !== null && dockerfileText.value !== dockerfileBase.value)
// The saved Dockerfile changed while there were edits.
const dockerfileStale = computed(() => dockerfileDirty.value && !!dockerfile.data.value && dockerfile.data.value.content !== dockerfileBase.value)
function discardDockerfile() {
  const d = dockerfile.data.value
  dockerfileText.value = d?.content ?? ''
  dockerfileBase.value = d?.content ?? null
}
// A GitHub app has its own card that says where the Dockerfile lives. The
// reason alone is shown where a Dockerfile is expected: type Dockerfile or a
// ready-made app (a static site does not need a card saying it has none).
const showDockerfile = computed(() => {
  const a = app.data.value
  const d = dockerfile.data.value
  if (!a || a.git_source) return false
  if (dockerfile.isError.value && !d) return true
  return !!d && (d.editable || !!d.content || a.build_mode === 'dockerfile' || !!a.catalog_id)
})

// A deployment that is queued or building: an update or a save now would queue
// behind it, and the version shown changes only once a deployment is live.
const deployments = useQuery({
  queryKey: computed(() => keys.deployments(props.slug)),
  queryFn: () => api.deployments(props.slug),
  refetchInterval: livePoll(REFRESH_MS),
})
const deploying = computed(() => (deployments.data.value ?? []).some((d) => !['running', 'failed', 'superseded', 'cancelled'].includes(d.state)))

function deployStarted(done: string, detail?: string) {
  toastOk(done, detail)
  void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
  void qc.invalidateQueries({ queryKey: keys.deployments(props.slug) })
  void qc.invalidateQueries({ queryKey: keys.apps })
  void router.push({ name: 'app-deployments', params: { slug: props.slug } })
}
const saveDockerfile = useMutation({
  mutationFn: () => api.saveDockerfile(props.slug, dockerfileText.value),
  onSuccess: () => {
    // The saved text is the new base; the refetch confirms it.
    dockerfileBase.value = dockerfileText.value
    void qc.invalidateQueries({ queryKey: keys.dockerfile(props.slug) })
    deployStarted('Dockerfile saved', 'A new version is being built.')
  },
  onError: (e) => toastError(e),
})

// ---------------------------------------------------------------- ready-made app
const catalogEntry = useCatalogApp(() => app.data.value?.catalog_id ?? '')
const catalogName = computed(() => catalogEntry.data.value?.name ?? app.data.value?.catalog_id ?? '')
const updateOpen = ref(false)
const catalogUpdate = useMutation({
  mutationFn: () => api.catalogUpdate(props.slug),
  onSuccess: () => {
    updateOpen.value = false
    // The update replaces the Dockerfile: drop local edits so the new one shows.
    dockerfileBase.value = null
    void qc.invalidateQueries({ queryKey: keys.dockerfile(props.slug) })
    deployStarted('Update started', 'A new version is being built.')
  },
  onError: (e) => {
    updateOpen.value = false
    toastError(e)
    void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
  },
})

// ---------------------------------------------------------------- delete
const deleteOpen = ref(false)
const del = useMutation({
  mutationFn: () => api.deleteApp(props.slug),
  onSuccess: async () => {
    const slug = props.slug
    toastOk('App deleted')
    deleteOpen.value = false
    // Leave first: removing the cache while this page still polls the app would flash a 404.
    await router.replace('/')
    for (const key of [keys.app(slug), keys.deployments(slug), keys.env(slug), keys.domains(slug)]) {
      queryClient.removeQueries({ queryKey: key })
    }
    queryClient.removeQueries({ queryKey: ['metrics', slug] })
    void qc.invalidateQueries({ queryKey: keys.apps })
    void qc.invalidateQueries({ queryKey: keys.me })
  },
  onError: (e) => toastError(e),
})
</script>

<template>
  <div class="grid gap-4">
    <Card>
      <CardHeader>
        <CardTitle>Name and address</CardTitle>
        <CardDescription>The name is only shown in bitploy. The subdomain is the address people open.</CardDescription>
      </CardHeader>
      <CardContent>
        <FormScope :form="generalForm">
          <form class="grid gap-4" novalidate @submit.prevent="submitGeneral">
            <div class="grid gap-4 sm:grid-cols-2 sm:items-start">
              <FormField v-slot="{ componentField }" name="name">
                <FormItem>
                  <FormLabel>Name</FormLabel>
                  <FormControl><Input autocomplete="off" v-bind="componentField" /></FormControl>
                  <FormMessage />
                </FormItem>
              </FormField>
              <FormField v-slot="{ componentField }" name="slug">
                <FormItem>
                  <FormLabel>Subdomain</FormLabel>
                  <div class="flex min-w-0 items-center gap-2">
                    <FormControl><Input autocapitalize="off" autocomplete="off" spellcheck="false" class="min-w-0 font-mono" v-bind="componentField" /></FormControl>
                    <span v-if="me.data.value" class="max-w-[50%] shrink-0 truncate font-mono text-sm text-muted-foreground">.{{ me.data.value.apps_domain }}</span>
                  </div>
                  <FormDescription>Changing it moves the app to a new address. The old one stays reserved for you for 30 days.</FormDescription>
                  <FormMessage />
                </FormItem>
              </FormField>
            </div>
            <div><Button type="submit" :disabled="saveGeneral.isPending.value">{{ saveGeneral.isPending.value ? 'Saving...' : 'Save' }}</Button></div>
          </form>
        </FormScope>
      </CardContent>
    </Card>

    <PasswordProtection v-if="app.data.value" :app="app.data.value" />

    <Card>
      <CardHeader>
        <CardTitle>Environment variables</CardTitle>
        <CardDescription>
          Passwords, API keys and other settings your app reads when it starts. Values are hidden after saving.
        </CardDescription>
      </CardHeader>
      <CardContent class="grid gap-4">
        <QueryError v-if="env.isError.value" :error="env.error.value" @retry="env.refetch()" />
        <Skeleton v-else-if="env.isPending.value" class="h-20 w-full" />
        <EmptyState v-else-if="!env.data.value?.length" :icon="KeyRound" title="No variables yet." description="Add one below, like DATABASE_URL." />
        <ul v-else class="divide-y rounded-lg border">
          <li v-for="v in env.data.value" :key="v.name" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
            <span class="font-mono text-sm font-medium">{{ v.name }}</span>
            <span class="font-mono text-sm text-muted-foreground">{{ v.masked }}</span>
            <span class="text-xs text-muted-foreground">changed <RelativeTime :date="v.updated_at" /></span>
            <div class="ml-auto flex gap-1">
              <Button size="sm" variant="ghost" :aria-label="`Change ${v.name}`" @click="editEnv(v.name)"><Pencil /> Change</Button>
              <Button size="sm" variant="ghost" class="text-destructive" :aria-label="`Remove ${v.name}`" @click="envToRemove = v.name"><Trash2 /> Remove</Button>
            </div>
          </li>
        </ul>

        <FormScope :form="envForm">
          <form id="env-form" class="grid gap-3 sm:grid-cols-[1fr_2fr_auto] sm:items-start" novalidate @submit.prevent="submitEnv">
            <FormField v-slot="{ componentField }" name="name">
              <FormItem>
                <FormLabel>Name</FormLabel>
                <FormControl><Input placeholder="API_KEY" autocapitalize="off" spellcheck="false" class="font-mono" v-bind="componentField" /></FormControl>
                <FormMessage />
              </FormItem>
            </FormField>
            <FormField v-slot="{ componentField }" name="value">
              <FormItem>
                <FormLabel>Value</FormLabel>
                <FormControl><Textarea rows="1" placeholder="Paste the secret value" autocomplete="off" spellcheck="false" class="min-h-8 resize-y py-[5px] leading-5 font-mono" v-bind="componentField" /></FormControl>
                <FormMessage />
              </FormItem>
            </FormField>
            <Button type="submit" class="sm:mt-[1.375rem]" :disabled="saveEnv.isPending.value">{{ saveEnv.isPending.value ? 'Saving...' : 'Save variable' }}</Button>
          </form>
        </FormScope>
      </CardContent>
    </Card>

    <Card v-if="app.data.value?.catalog_id">
      <CardHeader>
        <CardTitle>Ready-made app</CardTitle>
        <CardDescription>
          Based on {{ catalogName }} <span class="font-mono">{{ app.data.value.catalog_version }}</span>
        </CardDescription>
      </CardHeader>
      <CardContent v-if="app.data.value.catalog_update" class="flex flex-wrap items-center gap-3">
        <Button :disabled="catalogUpdate.isPending.value || deploying" @click="updateOpen = true">Update to {{ app.data.value.catalog_update }}</Button>
        <p class="text-sm text-muted-foreground">{{ deploying ? 'A deployment is in progress. Update when it is done.' : 'A newer version is in the catalog.' }}</p>
      </CardContent>
    </Card>

    <Card v-if="showDockerfile">
      <CardHeader>
        <CardTitle>Dockerfile</CardTitle>
        <CardDescription v-if="dockerfile.data.value?.editable">Change the image version or anything else, then save. Bitploy builds and deploys it. Data in /data is kept.</CardDescription>
        <CardDescription v-else-if="dockerfile.data.value">{{ dockerfile.data.value.reason }}</CardDescription>
      </CardHeader>
      <CardContent v-if="(dockerfile.isError.value && !dockerfile.data.value) || dockerfile.data.value?.editable || dockerfile.data.value?.content">
        <QueryError v-if="dockerfile.isError.value && !dockerfile.data.value" :error="dockerfile.error.value" @retry="dockerfile.refetch()" />
        <form v-else-if="dockerfile.data.value?.editable || dockerfile.data.value?.content" class="grid gap-4" novalidate @submit.prevent="saveDockerfile.mutate()">
          <DockerfileEditor v-model="dockerfileText" :readonly="!dockerfile.data.value?.editable" :disabled="saveDockerfile.isPending.value" />
          <p v-if="dockerfileStale" class="text-xs text-warning">The saved Dockerfile changed since you started editing. Discard to see it, or save to replace it.</p>
          <div v-if="dockerfile.data.value?.editable" class="flex flex-wrap items-center gap-3">
            <Button type="submit" :disabled="!dockerfileDirty || !dockerfileText.trim() || saveDockerfile.isPending.value">{{ saveDockerfile.isPending.value ? 'Saving...' : 'Save and deploy' }}</Button>
            <Button v-if="dockerfileDirty" type="button" variant="ghost" :disabled="saveDockerfile.isPending.value" @click="discardDockerfile">Discard</Button>
            <p v-if="dockerfileDirty" class="text-xs text-muted-foreground">Unsaved changes</p>
          </div>
        </form>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle>Type</CardTitle>
        <CardDescription>How bitploy builds your files. Changes apply on the next deploy.</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="grid gap-4" novalidate @submit.prevent="saveType.mutate()">
          <BuildTypeFields v-model:mode="typeMode" v-model:output-dir="typeOutput" later id-prefix="settings" />
          <div><Button type="submit" :disabled="!typeChanged || !!outputDirError(typeOutput) || saveType.isPending.value">{{ saveType.isPending.value ? 'Saving...' : 'Save' }}</Button></div>
        </form>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle>Size</CardTitle>
        <CardDescription v-if="app.data.value?.static">Static sites have a fixed size and do not count towards your memory and CPU allowance.</CardDescription>
        <CardDescription v-else>How much memory and processor power this app gets.</CardDescription>
      </CardHeader>
      <CardContent>
        <FormScope :form="resForm">
          <form class="grid gap-4" novalidate @submit.prevent="submitRes">
            <div class="grid gap-4 sm:grid-cols-2 sm:items-start">
              <FormField v-slot="{ componentField }" name="memory_mb">
                <FormItem>
                  <FormLabel>Memory (MB)</FormLabel>
                  <FormControl><Input type="number" min="64" step="64" inputmode="numeric" :disabled="isStatic" v-bind="componentField" /></FormControl>
                  <FormDescription v-if="room && quota && !isStatic">
                    Up to {{ formatMB(room.mem) }}<template v-if="room.otherMem">. Your other running apps have {{ formatMB(room.otherMem) }} of your {{ formatMB(quota.memory_mb) }}.</template><template v-else>.</template>
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              </FormField>
              <FormField v-slot="{ componentField }" name="cpus">
                <FormItem>
                  <FormLabel>Processor (CPUs)</FormLabel>
                  <FormControl><Input type="number" min="0.1" step="0.1" inputmode="decimal" :disabled="isStatic" v-bind="componentField" /></FormControl>
                  <FormDescription v-if="room && quota && !isStatic">
                    Up to {{ room.cpus }} CPUs<template v-if="room.otherCPU">. Your other running apps have {{ room.otherCPU / 1000 }} of your {{ quota.cpu_millis / 1000 }}.</template><template v-else>.</template>
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              </FormField>
            </div>
            <div><Button type="submit" :disabled="isStatic || saveRes.isPending.value">{{ saveRes.isPending.value ? 'Saving...' : 'Save' }}</Button></div>
          </form>
        </FormScope>
      </CardContent>
    </Card>

    <template v-if="app.data.value">
      <GitLinked v-if="app.data.value.git_source" :app="app.data.value" :source="app.data.value.git_source" />
      <GitConnect v-else :app="app.data.value" />
    </template>

    <Card class="border-destructive/40">
      <CardHeader>
        <CardTitle class="text-destructive">Delete this app</CardTitle>
        <CardDescription>This removes the app, its address, its stored files, its backups and all old versions. It cannot be undone.</CardDescription>
      </CardHeader>
      <CardContent>
        <Button variant="destructive" @click="deleteOpen = true"><Trash2 /> Delete app</Button>
      </CardContent>
    </Card>

    <ConfirmDialog
      v-model:open="updateOpen"
      :title="`Update to ${app.data.value?.catalog_update ?? ''}?`"
      :description="`Replaces the Dockerfile with the catalog's version ${app.data.value?.catalog_update ?? ''} and deploys it. Changes you made to the Dockerfile are lost. Data in /data is kept, but a new version may convert it so the old one cannot read it again: make a backup first.`"
      :confirm-label="`Update to ${app.data.value?.catalog_update ?? ''}`"
      :destructive="false"
      :loading="catalogUpdate.isPending.value"
      @confirm="catalogUpdate.mutate()"
    />
    <ConfirmDialog
      v-model:open="renameOpen"
      title="Change the address?"
      :description="renameDescription"
      confirm-label="Change address"
      :destructive="false"
      :loading="saveGeneral.isPending.value"
      @confirm="pendingRename && saveGeneral.mutate(pendingRename)"
    />
    <ConfirmDialog
      v-model:open="envRemoveOpen"
      title="Remove this variable?"
      :description="`${envToRemove ?? 'The variable'} will be gone the next time the app starts. Your app may stop working if it needs it.`"
      confirm-label="Remove"
      :loading="removeEnv.isPending.value"
      @confirm="envToRemove && removeEnv.mutate(envToRemove)"
    />
    <ConfirmDialog
      v-model:open="deleteOpen"
      title="Delete this app for good?"
      description="Everything about it is removed, including the files in /data and its backups."
      confirm-label="Delete app"
      :type-to-confirm="slug"
      :loading="del.isPending.value"
      @confirm="del.mutate()"
    />
  </div>
</template>
