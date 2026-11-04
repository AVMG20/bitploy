<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import FormScope from '@/components/FormScope.vue'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { ArrowUpCircle, Trash2 } from 'lucide-vue-next'
import DockerfileEditor from '@/components/DockerfileEditor.vue'
import BuildTypeFields from '@/components/BuildTypeFields.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import QueryError from '@/components/QueryError.vue'
import GitConnect from './GitConnect.vue'
import GitLinked from './GitLinked.vue'
import PasswordProtection from './PasswordProtection.vue'
import SettingsSection from '@/components/SettingsSection.vue'
import CatalogUpdateDialog from './CatalogUpdateDialog.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { REFRESH_MS, keys, livePoll, useApp, useCatalogApp, useMe } from '@/composables/queries'
import { api } from '@/lib/api'
import { outputDirError } from '@/lib/detect'
import { CPU_HINT, formatCPU, formatCPUMillis, formatMB } from '@/lib/format'
import { markRestartNeeded } from '@/composables/useRestartNeeded'
import { toastError, toastOk } from '@/lib/notify'
import { queryClient } from '@/lib/queryClient'
import type { BuildMode, UpdateAppInput } from '@/lib/types'

const props = defineProps<{ slug: string }>()
const router = useRouter()

// The page is grouped in sections; the links at the top jump to them.
const sections = [
  { id: 'general', label: 'General' },
  { id: 'build', label: 'Build' },
  { id: 'size', label: 'Size' },
  { id: 'delete', label: 'Delete app' },
]
function jump(id: string) {
  document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
const qc = useQueryClient()
const app = useApp(() => props.slug)
const me = useMe()
const quota = computed(() => me.data.value?.quota)

// A link to settings#build opens at that section once the page has drawn.
const route = useRoute()
watch(
  () => [route.hash, !!app.data.value] as const,
  ([hash, ready]) => {
    if (hash && ready) void nextTick(() => jump(hash.slice(1)))
  },
  { immediate: true },
)

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
  return `The app moves to ${to} and is down for a few seconds. The old address stops working, so links and bookmarks to it break. Saved files are kept.`
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
          .max(room.value?.cpus ?? 100, `You have ${room.value?.cpus ?? ''} CPU left for this app.`),
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
  onSuccess: (updated) => {
    toastOk('Size saved', app.data.value?.status === 'running' ? 'Restart the app to apply it.' : 'It applies the next time the app starts.')
    markRestartNeeded(app.data.value, 'size')
    resForm.resetForm({ values: { memory_mb: updated.memory_mb, cpus: updated.cpu_millis / 1000 } })
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
// The update replaces the Dockerfile: drop local edits so the new one shows.
function catalogUpdated() {
  dockerfileBase.value = null
}

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
  <div class="grid gap-10">
    <nav aria-label="Settings sections" class="-mb-4 flex flex-wrap items-center gap-1.5">
      <a
        v-for="sec in sections"
        :key="sec.id"
        :href="`#${sec.id}`"
        class="rounded-md border bg-card px-2.5 py-1 text-xs font-medium text-muted-foreground shadow-card transition-colors hover:bg-accent hover:text-foreground"
        @click.prevent="jump(sec.id)"
      >
        {{ sec.label }}
      </a>
    </nav>

    <SettingsSection id="general" class="scroll-mt-16" title="General" description="What the app is called in bitploy, where people open it, and who may see it.">
      <div class="grid gap-4">
        <Card>
          <CardHeader>
            <CardTitle>Name and address</CardTitle>
            <CardDescription>The name is only shown in bitploy. The address is where people open the app.</CardDescription>
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
                      <FormLabel>Address</FormLabel>
                      <div class="flex min-w-0 items-center gap-2">
                        <FormControl><Input autocapitalize="off" autocomplete="off" spellcheck="false" class="min-w-0 font-mono" v-bind="componentField" /></FormControl>
                        <span v-if="me.data.value" class="max-w-[50%] shrink-0 truncate font-mono text-sm text-muted-foreground">.{{ me.data.value.apps_domain }}</span>
                      </div>
                      <FormDescription>Changing it moves the app to a new address. The old one stays reserved for you for 30 days.</FormDescription>
                      <FormMessage />
                    </FormItem>
                  </FormField>
                </div>
                <div><Button type="submit" :disabled="saveGeneral.isPending.value || !generalForm.meta.value.dirty">{{ saveGeneral.isPending.value ? 'Saving...' : 'Save' }}</Button></div>
              </form>
            </FormScope>
          </CardContent>
        </Card>

        <PasswordProtection v-if="app.data.value" :app="app.data.value" />
      </div>
    </SettingsSection>

    <SettingsSection id="build" class="scroll-mt-16" title="Build" description="Where the code comes from and how bitploy turns it into a running app. Changes here apply on the next deploy.">
      <div class="grid gap-4">
        <Card v-if="app.data.value?.catalog_id">
          <CardHeader>
            <CardTitle>Ready-made app</CardTitle>
            <CardDescription>
              Based on {{ catalogName }} <span class="font-mono">{{ app.data.value.catalog_version }}</span>.
              <template v-if="!app.data.value.catalog_update">This is the newest version in the catalog.</template>
            </CardDescription>
          </CardHeader>
          <CardContent v-if="app.data.value.catalog_update" class="flex flex-wrap items-center gap-3">
            <Button :disabled="deploying" @click="updateOpen = true"><ArrowUpCircle /> Update to {{ app.data.value.catalog_update }}</Button>
            <p class="text-sm text-muted-foreground">{{ deploying ? 'A deployment is in progress. Update when it is done.' : 'A newer version is in the catalog.' }}</p>
          </CardContent>
        </Card>

        <template v-if="app.data.value">
          <GitLinked v-if="app.data.value.git_source" :app="app.data.value" :source="app.data.value.git_source" />
          <GitConnect v-else :app="app.data.value" />
        </template>

        <Card v-if="showDockerfile">
          <CardHeader>
            <CardTitle>Dockerfile</CardTitle>
            <CardDescription v-if="dockerfile.data.value?.editable">Change the image version or anything else, then save. Bitploy builds and deploys it. Saved files are kept.</CardDescription>
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
      </div>
    </SettingsSection>

    <SettingsSection id="size" class="scroll-mt-16" title="Size" :description="`How much memory and CPU the app may use. ${CPU_HINT} A change applies after a restart.`">
      <Card>
        <CardContent>
          <p v-if="isStatic" class="mb-4 text-sm text-muted-foreground">Static sites have a fixed size and do not count towards your memory and CPU allowance.</p>
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
                    <FormLabel>CPU</FormLabel>
                    <FormControl><Input type="number" min="0.1" step="0.1" inputmode="decimal" :disabled="isStatic" v-bind="componentField" /></FormControl>
                    <FormDescription v-if="room && quota && !isStatic">
                      Up to {{ formatCPU(room.cpus) }}<template v-if="room.otherCPU">. Your other running apps have {{ formatCPUMillis(room.otherCPU) }} of your {{ formatCPUMillis(quota.cpu_millis) }}.</template><template v-else>.</template>
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                </FormField>
              </div>
              <div><Button type="submit" :disabled="isStatic || saveRes.isPending.value || !resForm.meta.value.dirty">{{ saveRes.isPending.value ? 'Saving...' : 'Save size' }}</Button></div>
            </form>
          </FormScope>
        </CardContent>
      </Card>
    </SettingsSection>

    <SettingsSection id="delete" class="scroll-mt-16" title="Delete app" description="Removes the app, its address, its saved files, its backups and all old versions.">
      <Card class="border-destructive/40">
        <CardContent class="flex flex-wrap items-center justify-between gap-3">
          <p class="text-sm text-muted-foreground">This cannot be undone. Download a backup first if you may need the data.</p>
          <Button variant="destructive" @click="deleteOpen = true"><Trash2 /> Delete {{ app.data.value?.name ?? 'app' }}</Button>
        </CardContent>
      </Card>
    </SettingsSection>

    <CatalogUpdateDialog v-if="app.data.value?.catalog_update && updateOpen" v-model:open="updateOpen" :app="app.data.value" @updated="catalogUpdated" />
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
      v-model:open="deleteOpen"
      :title="`Delete ${app.data.value?.name ?? 'this app'} for good?`"
      description="Everything about it is removed, including its saved files and its backups."
      confirm-label="Delete app"
      :type-to-confirm="slug"
      :loading="del.isPending.value"
      @confirm="del.mutate()"
    />
  </div>
</template>
