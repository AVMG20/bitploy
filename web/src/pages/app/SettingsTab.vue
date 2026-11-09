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
import DangerRow from '@/components/DangerRow.vue'
import DangerZone from '@/components/DangerZone.vue'
import DeleteAppDialog from '@/components/DeleteAppDialog.vue'
import FormCard from '@/components/FormCard.vue'
import QueryError from '@/components/QueryError.vue'
import GitConnect from './GitConnect.vue'
import GitLinked from './GitLinked.vue'
import PasswordProtection from './PasswordProtection.vue'
import PublicPort from './PublicPort.vue'
import SettingsSection from '@/components/SettingsSection.vue'
import DomainSelect from '@/components/DomainSelect.vue'
import CatalogUpdateDialog from './CatalogUpdateDialog.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { REFRESH_MS, keys, livePoll, useApp, useCatalogApp, useMe, useProjects, useServiceTemplates } from '@/composables/queries'
import ProjectSettings from './ProjectSettings.vue'
import RunSettings from './RunSettings.vue'
import { api } from '@/lib/api'
import { outputDirError } from '@/lib/detect'
import { ROOT_ADDRESS, addressError } from '@/lib/slug'
import { CPU_HINT, formatCPU, formatCPUMillis, formatMB } from '@/lib/format'
import { markRestartNeeded } from '@/composables/useRestartNeeded'
import { toastError, toastOk } from '@/lib/notify'
import { queryClient } from '@/lib/queryClient'
import type { BuildMode, UpdateAppInput } from '@/lib/types'
import ServiceIcon from '@/components/ServiceIcon.vue'
import { isMulti, serviceRole, sortServices } from '@/lib/services'
import DeployCommands from './DeployCommands.vue'
import { offersDeployCommands } from '@/lib/deployCommands'

const props = defineProps<{ slug: string }>()
const router = useRouter()

// The page is grouped in sections; the links at the top jump to them.
const sections = computed(() => [
  { id: 'general', label: 'General' },
  ...(inProjects.value ? [{ id: 'project', label: 'Project' }] : []),
  { id: 'build', label: app.data.value?.template ? 'Image' : 'Build' },
  ...(showDeployCommands.value ? [{ id: 'deploy-commands', label: 'Deploy commands' }] : []),
  { id: 'size', label: 'Size' },
  { id: 'danger', label: 'Danger zone' },
])
function jump(id: string) {
  document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
const qc = useQueryClient()
const app = useApp(() => props.slug)
const me = useMe()
const quota = computed(() => me.data.value?.quota)
// Several services: build and size come from the compose file, one per service.
const multi = computed(() => isMulti(app.data.value))
const showDeployCommands = computed(() => offersDeployCommands(app.data.value))
// Without projects on the server each app is its own project: no short name to rename.
const projectList = useProjects()
const templates = useServiceTemplates()
// A database, cache or search engine: no web address, nothing to protect with a password.
// No web address: a database or cache, or a worker. Its slug is only an ID.
const isPrivate = computed(() => !!app.data.value?.template || app.data.value?.type === 'worker')
const templateOf = computed(() => templates.data.value?.find((t) => t.id === app.data.value?.template) ?? null)
const inProjects = computed(() => !!projectList.data.value && !projectList.data.value.fallback)
const services = computed(() => sortServices(app.data.value?.services ?? []))
const totalMB = computed(() => services.value.reduce((n, s) => n + s.memory_mb, 0))
const totalCPU = computed(() => services.value.reduce((n, s) => n + s.cpu_millis, 0))

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
const domain = ref('')
// The domain picked next to the Address field, as whoami lists it.
const domainChoice = computed(() => {
  const list = me.data.value?.app_domains ?? []
  return list.find((d) => d.domain === domain.value) ?? (domain.value ? undefined : list.find((d) => d.default))
})
const shownDomain = computed(() => domain.value || me.data.value?.apps_domain || '')
// An admin may give the app the domain itself (@) where whoami offers it; an
// app that already has it keeps @ on that domain.
const rootAllowed = computed(
  () => !isPrivate.value && (!!domainChoice.value?.root_allowed || (!!app.data.value?.root && domain.value === app.data.value.domain)),
)
const isAdmin = computed(() => me.data.value?.user.role === 'admin' && me.data.value.scope === 'session')
const rootRefusal = computed(() => (isAdmin.value && !isPrivate.value ? `${shownDomain.value} itself cannot be an app's address. Type a name.` : undefined))
const addressOf = (a: { slug: string; root: boolean }) => (a.root ? ROOT_ADDRESS : a.slug)

const generalForm = useForm({
  validationSchema: computed(() =>
    toTypedSchema(
      z.object({
        name: z.string().trim().min(1, 'Give the app a name.').max(80, 'Use at most 80 characters.'),
        slug: z
          .string()
          .trim()
          .toLowerCase()
          .superRefine((s, ctx) => {
            const message = addressError(s, rootAllowed.value, rootRefusal.value)
            if (message) ctx.addIssue({ code: z.ZodIssueCode.custom, message })
          }),
      }),
    ),
  ),
  initialValues: { name: '', slug: '' },
})

let generalLoadedFor = ''
watch(
  () => app.data.value,
  (a) => {
    if (a && generalLoadedFor !== a.id) {
      generalLoadedFor = a.id
      generalForm.resetForm({ values: { name: a.name, slug: addressOf(a) } })
      domain.value = a.domain
    }
  },
  { immediate: true },
)
const domainChanged = computed(() => !!domain.value && !!app.data.value && domain.value !== app.data.value.domain)
const typedRoot = computed(() => (generalForm.values.slug ?? '').trim() === ROOT_ADDRESS)
// Another app has the domain itself now: the server refuses @ until it moves.
const rootHolder = computed(() => {
  const holder = domainChoice.value?.root_app
  return holder && holder !== props.slug ? holder : ''
})

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
    const a = app.data.value
    if (v.name !== a?.name) input.name = v.name
    // @ is sent while it changes something; any name leaves the domain itself.
    if (v.slug === ROOT_ADDRESS ? !a?.root || domainChanged.value : v.slug !== props.slug || a?.root) input.slug = v.slug
    if (domainChanged.value) input.domain = domain.value
    return api.updateApp(props.slug, input)
  },
  onSuccess: async (updated) => {
    const oldSlug = props.slug
    const before = app.data.value
    const moved = updated.domain !== before?.domain || updated.root !== before?.root
    // Getting the domain itself on the same domain keeps <slug>.<domain> working.
    const kept = updated.root && updated.domain === before?.domain
    pendingRename.value = null
    generalForm.resetForm({ values: { name: updated.name, slug: addressOf(updated) } })
    domain.value = updated.domain
    void qc.invalidateQueries({ queryKey: keys.apps })
    void qc.invalidateQueries({ queryKey: keys.me })
    if (updated.slug === oldSlug) {
      const detail = kept ? `The app is at ${updated.url}. ${updated.slug}.${updated.domain} keeps working too.` : `The app is at ${updated.url}. The old address stops working.`
      toastOk(moved ? 'Address changed' : 'Name saved', moved && updated.url ? detail : undefined)
      void qc.invalidateQueries({ queryKey: keys.app(oldSlug) })
      return
    }
    toastOk(isPrivate.value ? 'ID changed' : 'Address changed', updated.url && !isPrivate.value ? `The app is at ${updated.url}. The old address stops working.` : undefined)
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
  const a = app.data.value
  if (v.slug !== (a ? addressOf(a) : props.slug) || domainChanged.value) pendingRename.value = v
  else if (v.name !== a?.name) saveGeneral.mutate(v)
})
const renameDescription = computed(() => {
  if (isPrivate.value) return 'Links to this page and API calls that use the old ID stop working. The other apps keep reaching it by its short name.'
  const next = pendingRename.value?.slug ?? ''
  const d = shownDomain.value
  if (next === ROOT_ADDRESS) {
    return domainChanged.value
      ? `The app moves to ${d}, and ${props.slug}.${d} works too. The old address stops working, so links and bookmarks to it break.`
      : `The app gets ${d} as its address. ${props.slug}.${d} keeps working too.`
  }
  if (app.data.value?.root && next === props.slug && !domainChanged.value) {
    return `The app moves to ${next}.${d}. ${d} itself stops working, so links and bookmarks to it break.`
  }
  return `The app moves to ${next}.${d} and is down for a few seconds. The old address stops working, so links and bookmarks to it break.`
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
  // Overallocating: nothing is checked, so there is no room to show or enforce.
  if (!q || !u || !a || q.overallocate) return null
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
const deleteText = computed(() => {
  const a = app.data.value
  if (a?.template) {
    return `Removes ${templateOf.value?.name ?? 'the service'} with all its data and backups. Download a backup first if you may need its data.`
  }
  const address = a?.type === 'worker' ? '' : ' its address,'
  return `Removes the app${multi.value ? ' with all its services' : ''},${address} its saved files, its backups and all old versions. Download a backup first if you may need its saved files.`
})
</script>

<template>
  <!-- Until the app is known the page cannot tell a ready-made database from an app built from code. -->
  <div v-if="!app.data.value" class="grid gap-10" data-testid="settings-loading">
    <QueryError v-if="app.isError.value" :error="app.error.value" @retry="app.refetch()" />
    <template v-else>
      <Skeleton class="h-7 w-80" />
      <Skeleton class="h-56 w-full" />
      <Skeleton class="h-40 w-full" />
    </template>
  </div>
  <div v-else class="grid gap-10">
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

    <SettingsSection id="general" class="scroll-mt-16" title="General" :description="isPrivate ? 'What the app is called in bitploy.' : 'Its name, its address and who may open it.'">
      <div class="grid gap-4">
        <FormScope :form="generalForm">
          <form novalidate @submit.prevent="submitGeneral">
            <FormCard
              :title="isPrivate ? 'Name and ID' : 'Name and address'"
              :description="isPrivate ? 'The name is only shown in bitploy. The ID is in the links to this page and in the API.' : 'The name is only shown in bitploy.'"
            >
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
                    <FormLabel>{{ isPrivate ? 'ID' : 'Address' }}</FormLabel>
                    <div class="flex min-w-0 items-center gap-2">
                      <FormControl><Input autocapitalize="off" autocomplete="off" spellcheck="false" class="min-w-0 font-mono" v-bind="componentField" /></FormControl>
                      <DomainSelect v-if="me.data.value && app.data.value && !isPrivate" v-model="domain" :current="app.data.value.domain" />
                    </div>
                    <FormDescription v-if="isPrivate">It has no web address. The other apps reach it by its short name, below.</FormDescription>
                    <template v-else-if="typedRoot && rootAllowed">
                      <FormDescription v-if="rootHolder" class="text-warning" data-testid="root-holder">{{ shownDomain }} is the address of {{ rootHolder }} now. Give that app another address first.</FormDescription>
                      <FormDescription v-else-if="domainChoice?.root_dns" data-testid="root-dns">{{ domainChoice.root_dns }}</FormDescription>
                      <FormDescription v-if="!rootHolder">The app answers on {{ shownDomain }} and on {{ props.slug }}.{{ shownDomain }}.</FormDescription>
                    </template>
                    <template v-else>
                      <FormDescription>Changing it moves the app to a new address. The old one stays reserved for you for 30 days.</FormDescription>
                      <FormDescription v-if="isAdmin && domainChoice?.root_allowed" data-testid="root-hint">@ uses the domain itself: {{ shownDomain }}</FormDescription>
                    </template>
                    <FormMessage />
                  </FormItem>
                </FormField>
              </div>
              <template #actions>
                <Button type="submit" :disabled="saveGeneral.isPending.value || !(generalForm.meta.value.dirty || domainChanged)">{{ saveGeneral.isPending.value ? 'Saving...' : isPrivate ? 'Save name' : 'Save name and address' }}</Button>
              </template>
            </FormCard>
          </form>
        </FormScope>

        <PasswordProtection v-if="app.data.value && !isPrivate" :app="app.data.value" />
        <PublicPort v-if="app.data.value?.public_port && app.data.value.public_port.reason !== 'web_app'" :app="app.data.value" />
      </div>
    </SettingsSection>

    <SettingsSection v-if="inProjects && app.data.value" id="project" class="scroll-mt-16" title="Project" description="The short name the other apps of the project use to reach this one.">
      <ProjectSettings :app="app.data.value" />
    </SettingsSection>

    <SettingsSection v-if="app.data.value?.template" id="build" class="scroll-mt-16" title="Image" description="This runs a ready-made image. There is no code to build.">
      <Card>
        <CardContent class="grid gap-1 text-sm">
          <p>
            Runs <span class="font-mono">{{ templateOf?.image ?? app.data.value.template }}</span>
            <template v-if="templateOf">, the ready-made {{ templateOf.name }}</template>.
          </p>
        </CardContent>
      </Card>
    </SettingsSection>

    <SettingsSection v-else id="build" class="scroll-mt-16" title="Build" description="Where the code comes from and how bitploy turns it into a running app.">
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

        <Card v-if="multi">
          <CardHeader>
            <CardTitle>Compose file</CardTitle>
            <CardDescription>
              This app runs {{ services.length }} services from <span class="font-mono">{{ app.data.value?.compose_path || 'compose.yaml' }}</span>.
              To add a service or change one, edit the file and deploy again.
            </CardDescription>
          </CardHeader>
        </Card>

        <Card v-if="showDockerfile && !multi">
          <CardHeader>
            <CardTitle>Dockerfile</CardTitle>
            <CardDescription v-if="dockerfile.data.value?.editable">Change the image version or anything else, then save. bitploy builds and deploys it.</CardDescription>
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

        <form v-if="!multi" novalidate @submit.prevent="saveType.mutate()">
          <FormCard title="Build type" description="How bitploy builds your files.">
            <BuildTypeFields v-model:mode="typeMode" v-model:output-dir="typeOutput" later id-prefix="settings" />
            <template #hint>Applies on the next deploy.</template>
            <template #actions>
              <Button type="submit" :disabled="!typeChanged || !!outputDirError(typeOutput) || saveType.isPending.value">{{ saveType.isPending.value ? 'Saving...' : 'Save build type' }}</Button>
            </template>
          </FormCard>
        </form>

        <RunSettings v-if="!multi && app.data.value" :app="app.data.value" />
      </div>
    </SettingsSection>

    <DeployCommands v-if="showDeployCommands && app.data.value" :app="app.data.value" />

    <SettingsSection
      v-if="multi"
      id="size"
      class="scroll-mt-16"
      title="Size"
      :description="`How much memory and CPU each service may use. ${CPU_HINT} They are set in your compose file (mem_limit and cpus); change them there and deploy again.`"
    >
      <Card class="py-0">
        <CardContent class="px-0">
          <ul class="divide-y">
            <li v-for="s in services" :key="s.name" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5 text-sm">
              <ServiceIcon :role="serviceRole(s).key" size="sm" />
              <span class="font-mono font-medium">{{ s.name }}</span>
              <span class="text-xs text-muted-foreground">{{ serviceRole(s).label }}</span>
              <span class="ml-auto tabular-nums">{{ formatMB(s.memory_mb) }} <span class="text-muted-foreground">memory,</span> {{ formatCPUMillis(s.cpu_millis) }}</span>
            </li>
            <li class="flex flex-wrap items-center gap-x-3 gap-y-1 bg-muted/30 px-4 py-2.5 text-sm">
              <span class="font-medium">Together</span>
              <span class="ml-auto font-medium tabular-nums">{{ formatMB(totalMB) }} <span class="font-normal text-muted-foreground">memory,</span> {{ formatCPUMillis(totalCPU) }}</span>
            </li>
          </ul>
        </CardContent>
      </Card>
    </SettingsSection>

    <SettingsSection v-else id="size" class="scroll-mt-16" title="Size" :description="`How much memory and CPU the app may use. ${CPU_HINT}`">
      <FormScope :form="resForm">
        <form novalidate @submit.prevent="submitRes">
          <FormCard>
            <p v-if="isStatic" class="mb-4 text-sm text-muted-foreground">Static sites have a fixed size and do not count towards your memory and CPU allowance.</p>
          <div class="grid gap-4 sm:grid-cols-2 sm:items-start">
            <FormField v-slot="{ componentField }" name="memory_mb">
              <FormItem>
                <FormLabel>Memory (MB)</FormLabel>
                <FormControl><Input type="number" min="64" step="64" inputmode="numeric" :disabled="isStatic" v-bind="componentField" /></FormControl>
                <FormDescription v-if="room && quota && !isStatic">
                  <template v-if="room.mem === 0">Your other running apps already have all of your {{ formatMB(quota.memory_mb) }}, so this app cannot get more. Stop one, or ask your admin for more.</template>
                  <template v-else>Up to {{ formatMB(room.mem) }}<template v-if="room.otherMem">. Your other running apps have {{ formatMB(room.otherMem) }} of your {{ formatMB(quota.memory_mb) }}.</template><template v-else>.</template></template>
                </FormDescription>
                <FormMessage />
              </FormItem>
            </FormField>
            <FormField v-slot="{ componentField }" name="cpus">
              <FormItem>
                <FormLabel>CPU</FormLabel>
                <FormControl><Input type="number" min="0.1" step="0.1" inputmode="decimal" :disabled="isStatic" v-bind="componentField" /></FormControl>
                <FormDescription v-if="room && quota && !isStatic">
                  <template v-if="room.cpus === 0">Your other running apps already have all of your {{ formatCPUMillis(quota.cpu_millis) }}, so this app cannot get more. Stop one, or ask your admin for more.</template>
                  <template v-else>Up to {{ formatCPU(room.cpus) }}<template v-if="room.otherCPU">. Your other running apps have {{ formatCPUMillis(room.otherCPU) }} of your {{ formatCPUMillis(quota.cpu_millis) }}.</template><template v-else>.</template></template>
                </FormDescription>
                <FormMessage />
              </FormItem>
            </FormField>
          </div>
            <template #hint>Applies after a restart.</template>
            <template #actions>
              <Button type="submit" :disabled="isStatic || saveRes.isPending.value || !resForm.meta.value.dirty">{{ saveRes.isPending.value ? 'Saving...' : 'Save size' }}</Button>
            </template>
          </FormCard>
        </form>
      </FormScope>
    </SettingsSection>

    <SettingsSection id="danger" class="scroll-mt-16" title="Danger zone" description="Actions that cannot be undone.">
      <DangerZone>
        <DangerRow
          :title="`Delete ${app.data.value?.name ?? 'this app'}`"
          :description="deleteText"
        >
          <Button variant="destructive" :disabled="!app.data.value" @click="deleteOpen = true"><Trash2 /> {{ app.data.value?.template ? 'Delete' : 'Delete app' }}</Button>
        </DangerRow>
      </DangerZone>
    </SettingsSection>

    <CatalogUpdateDialog v-if="app.data.value?.catalog_update && updateOpen" v-model:open="updateOpen" :app="app.data.value" @updated="catalogUpdated" />
    <ConfirmDialog
      v-model:open="renameOpen"
      :title="isPrivate ? 'Change the ID?' : 'Change the address?'"
      :description="renameDescription"
      :confirm-label="isPrivate ? 'Change ID' : 'Change address'"
      :destructive="false"
      :loading="saveGeneral.isPending.value"
      @confirm="pendingRename && saveGeneral.mutate(pendingRename)"
    />
    <DeleteAppDialog v-if="app.data.value" v-model:open="deleteOpen" :app="app.data.value" />
  </div>
</template>
