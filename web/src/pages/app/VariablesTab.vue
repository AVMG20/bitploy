<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { ChevronDown, ChevronUp, ClipboardPaste, KeyRound, Lock, Pencil, Plus, SearchX, Trash2 } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import FormScope from '@/components/FormScope.vue'
import ListSearch from '@/components/ListSearch.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { keys, useApp, useProject } from '@/composables/queries'
import ConnectionCard from '@/components/project/ConnectionCard.vue'
import { appRoute } from '@/lib/appLinks'
import { aliasOf, appRole, projectIdOf, referenceParts, referenceText } from '@/lib/projects'
import type { EnvVar } from '@/lib/types'
import { markRestartNeeded } from '@/composables/useRestartNeeded'
import { ApiError, api } from '@/lib/api'
import { parseDotenv } from '@/lib/dotenv'
import { isServedSite, variableNeedsDeploy } from '@/lib/buildVars'
import { looksSecretName } from '@/lib/secretName'
import { isServerDb } from '@/lib/dbvalue'
import { toastError, toastOk } from '@/lib/notify'
import { isMulti, serviceRole, sortServices } from '@/lib/services'
import { useQueryParam } from '@/composables/useQueryParam'
import type { Ref } from 'vue'

// Environment variables: settings and secrets the app reads when it starts.
// The list shows each value, except a hidden one: that is write-only and shows
// as dots. A change applies after a restart, which the app page offers as soon
// as one is saved.
const props = defineProps<{
  slug: string
  /** Only this service's own variables (the service panel); no scope picker then. */
  service?: string
}>()
const qc = useQueryClient()
const app = useApp(() => props.slug)
const env = useQuery({
  queryKey: computed(() => keys.env(props.slug)),
  queryFn: () => api.env(props.slug),
})

// A project with several services has shared variables (every service gets
// them) and each service's own, which win over a shared one of the same name.
const multi = computed(() => isMulti(app.data.value))
const services = computed(() => sortServices(app.data.value?.services ?? []))
const pickedScope: Ref<string> = props.service !== undefined ? ref('') : useQueryParam('scope', '')
const scope = computed(() => props.service ?? (multi.value && services.value.some((s) => s.name === pickedScope.value) ? pickedScope.value : ''))
const all = computed(() => env.data.value ?? [])
const list = computed(() => all.value.filter((v) => (v.service ?? '') === scope.value))
const names = computed(() => new Set(list.value.map((v) => v.name)))
const byName = computed(() => new Map(list.value.map((v) => [v.name, v])))
// In a service's scope: the shared ones it also gets.
const inherited = computed(() => (scope.value ? all.value.filter((v) => !v.service) : []))
const forWhom = computed(() => (scope.value ? ` for ${scope.value}` : multi.value ? ' for every service' : ''))
// A server without per-service variables refuses the field; say so in plain words.
function saveError(e: unknown) {
  if (scope.value && e instanceof ApiError && e.status === 422 && /service/.test(e.message)) {
    toastError(new Error(`This server cannot store variables for one service. Add it under Shared; every service gets those.`))
  } else toastError(e)
}
const scopes = computed(() => [
  { value: '', label: 'Shared', sub: 'every service', count: all.value.filter((v) => !v.service).length },
  ...services.value.map((s) => ({ value: s.name, label: s.name, sub: serviceRole(s).label, count: all.value.filter((v) => v.service === s.name).length })),
])

// A database, cache or search engine made from a template: its own variables
// are the settings its server starts with, shown folded under the card that
// tells how other apps connect to it.
const serverApp = computed(() => !!app.data.value?.template && !props.service)
const showServer = ref(false)
const serverWhat = computed(() => (app.data.value ? appRole(app.data.value).label.toLowerCase() : 'database'))
// PostgreSQL, MariaDB and MySQL read the user, password and database name only
// when they create their files: a later change does not reach them.
const setOnce = computed(() => isServerDb(app.data.value))
const serverBlurb = computed(() =>
  setOnce.value
    ? 'What this database really runs with: the user, password and database name it was created with. Your other apps do not need these, they use the shortcuts above. Changing one here does not change the database, and breaks the apps connected to it.'
    : 'What this server really runs with, like its password. Your other apps do not need these, they use the shortcuts above. A change applies after a restart.',
)

// ---------------------------------------------------------------- references to the other apps of the project
const { project } = useProject(() => (app.data.value ? projectIdOf(app.data.value) : ''))
const byAlias = computed(() => new Map((project.value?.apps ?? []).map((a) => [aliasOf(a), a])))
/**
 * The references in a value as chips naming the other app: ${{postgres.DATABASE_URL}}
 * shows as postgres.DATABASE_URL. The server masks the text around them
 * ("••••${{api.HOST}}••••"), so the row shows that there is more to the value.
 */
function valueParts(v: EnvVar) {
  const src = v.value ?? (v.masked.includes('${{') ? v.masked : v.references?.length ? v.references.join(' ') : v.masked)
  const parts = referenceParts(src).filter((p) => 'ref' in p || p.text.trim())
  return parts.some((p) => 'ref' in p) ? parts : null
}
// Quick picks under the form: the main variable of each database or cache in the project.
const suggestions = computed(() =>
  (project.value?.apps ?? [])
    .filter((a) => a.template && a.slug !== props.slug)
    .map((a) => {
      const role = appRole(a)
      const name = a.main_var || a.connection_vars?.[0] || (role.key === 'cache' ? 'REDIS_URL' : role.key === 'search' ? 'MEILI_URL' : 'DATABASE_URL')
      return { app: a, name, value: referenceText(aliasOf(a), name) }
    })
    .filter((sg) => !names.value.has(sg.name)),
)
function pickSuggestion(s: { name: string; value: string }) {
  addForm.setValues({ name: s.name, value: s.value })
}

const willRestart = computed(() => app.data.value?.status === 'running')
// On the app page the banner above the tabs asks for the restart (and the
// Restart button shows a dot); the toast only repeats it in the service panel,
// which covers that banner.
const appliesWhen = computed(() => (!willRestart.value ? 'The app uses it the next time it starts.' : props.service !== undefined ? 'Restart the app to use it.' : undefined))

// ---------------------------------------------------------------- search (only worth it for a longer list)
const search = ref('')
const shown = computed(() => {
  const q = search.value.trim().toLowerCase()
  return q ? list.value.filter((v) => v.name.toLowerCase().includes(q)) : list.value
})
const shownInherited = computed(() => {
  const q = search.value.trim().toLowerCase()
  return q ? inherited.value.filter((v) => v.name.toLowerCase().includes(q)) : inherited.value
})

// ---------------------------------------------------------------- add
const NAME_RULE = /^[A-Za-z_][A-Za-z0-9_]*$/
const addForm = useForm({
  validationSchema: toTypedSchema(
    z.object({
      name: z.string().trim().min(1, 'Give the variable a name.').regex(NAME_RULE, 'Use letters, numbers and underscores only, like API_KEY.'),
      value: z.string(),
    }),
  ),
  initialValues: { name: '', value: '' },
})
const addingExisting = computed(() => names.value.has((addForm.values.name ?? '').trim()))
// Hidden follows the name (SECRET, TOKEN, KEY, PASSWORD) until the box is clicked.
const addHiddenPicked = ref<boolean | null>(null)
const addHidden = computed(() => {
  if (addHiddenPicked.value !== null) return addHiddenPicked.value
  const name = (addForm.values.name ?? '').trim()
  return !!byName.value.get(name)?.hidden || looksSecretName(name)
})

// ---------------------------------------------------------------- build
// Every variable reaches the build too. A change to one the live version's
// build read (any variable of a site bitploy serves) needs a new build, not a
// restart.
const site = computed(() => isServedSite(app.data.value))
const buildApplies = 'The build reads it. Deploy again to use it.'
/** Note what a change of these variables waits for: a new build or a restart. */
function changed(names: string[]) {
  for (const n of names) markRestartNeeded(app.data.value, variableNeedsDeploy(app.data.value, n) ? 'build' : 'variables')
  void qc.invalidateQueries({ queryKey: keys.env(props.slug) })
}
/** The toast's second line for a change of these variables. */
function appliesFor(names: string[]) {
  return app.data.value?.current_deployment && names.some((n) => variableNeedsDeploy(app.data.value, n)) ? buildApplies : appliesWhen.value
}

/** value undefined: only the hidden flag changes, and the app needs no restart. */
const save = useMutation({
  mutationFn: (v: { name: string; value?: string; hidden: boolean }) =>
    api.setEnv(props.slug, v.value === undefined ? {} : { [v.name]: v.value }, [], scope.value || undefined, { [v.name]: v.hidden }),
  onSuccess: (_r, v) => {
    if (v.value === undefined) {
      toastOk(v.hidden ? `${v.name} is hidden` : `${v.name} is readable`)
      void qc.invalidateQueries({ queryKey: keys.env(props.slug) })
      return
    }
    toastOk(`Saved ${v.name}${forWhom.value}`, appliesFor([v.name]))
    changed([v.name])
  },
  onError: saveError,
})
const submitAdd = addForm.handleSubmit((v) =>
  save.mutate(
    { name: v.name.trim(), value: v.value, hidden: addHidden.value },
    {
      onSuccess: () => {
        addForm.resetForm({ values: { name: '', value: '' } })
        addHiddenPicked.value = null
        // Ready for the next one. After the fields are empty on the page: moving the
        // focus earlier makes the value box report its old text as a change and keep it.
        void nextTick(() => document.querySelector<HTMLInputElement>('#env-add input')?.focus())
      },
    },
  ),
)
function onAddKey(e: KeyboardEvent) {
  // Enter saves; Shift+Enter starts a new line (for a key that spans lines).
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    void submitAdd()
  }
}

// ---------------------------------------------------------------- edit one, in its row
// A readable value starts in the box; a hidden one cannot be read, so the box
// starts empty and turning it readable needs the value again.
const editing = ref<string | null>(null)
const editValue = ref('')
const editHidden = ref(false)
const editVar = computed(() => (editing.value ? byName.value.get(editing.value) : undefined))
const editNeedsValue = computed(() => !!editVar.value?.hidden && !editHidden.value && editValue.value === '')
async function startEdit(v: EnvVar) {
  editing.value = v.name
  editValue.value = v.value ?? ''
  editHidden.value = v.hidden
  await nextTick()
  document.querySelector<HTMLTextAreaElement>(`#env-edit-${v.name}`)?.focus()
}
function cancelEdit() {
  editing.value = null
  editValue.value = ''
}
function saveEdit() {
  const v = editVar.value
  if (!v || editNeedsValue.value) return
  // An empty box for a hidden value keeps the value; so does the same text.
  const same = v.hidden ? editValue.value === '' : editValue.value === v.value
  if (same && editHidden.value === v.hidden) return cancelEdit()
  save.mutate({ name: v.name, value: same ? undefined : editValue.value, hidden: editHidden.value }, { onSuccess: cancelEdit })
}
function onEditKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault()
    cancelEdit()
  } else if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    saveEdit()
  }
}

// ---------------------------------------------------------------- remove
const toRemove = ref<string | null>(null)
const removeOpen = computed({
  get: () => toRemove.value !== null,
  set: (v) => {
    if (!v) toRemove.value = null
  },
})
const remove = useMutation({
  mutationFn: (name: string) => api.setEnv(props.slug, {}, [name], scope.value || undefined),
  onSuccess: (_r, name) => {
    toastOk(`Removed ${name}`)
    toRemove.value = null
    changed([name])
  },
  onError: saveError,
})

// ---------------------------------------------------------------- paste a .env file
const pasteOpen = ref(false)
const pasteText = ref('')
const parsed = computed(() => parseDotenv(pasteText.value))
const replacing = computed(() => parsed.value.vars.filter((v) => names.value.has(v.name)).length)
// Each line follows the name rule of the add form; a hidden variable stays hidden.
const pasteHidden = computed(() => Object.fromEntries(parsed.value.vars.map((v) => [v.name, !!byName.value.get(v.name)?.hidden || looksSecretName(v.name)])))
const pasteHiddenCount = computed(() => Object.values(pasteHidden.value).filter(Boolean).length)
function openPaste() {
  pasteText.value = ''
  pasteOpen.value = true
}
const importAll = useMutation({
  mutationFn: () =>
    api.setEnv(props.slug, Object.fromEntries(parsed.value.vars.map((v) => [v.name, v.value])), [], scope.value || undefined, pasteHidden.value),
  onSuccess: () => {
    const names = parsed.value.vars.map((v) => v.name)
    toastOk(`Saved ${names.length} ${names.length === 1 ? 'variable' : 'variables'}${forWhom.value}`, appliesFor(names))
    pasteOpen.value = false
    changed(names)
  },
  onError: saveError,
})
</script>

<template>
  <div class="grid gap-4">
    <ConnectionCard v-if="serverApp" :app="app.data.value!" />
    <!-- A database's own variables are what its server starts with: folded away, the card above is what other apps use. -->
    <Card v-if="serverApp && !showServer">
      <CardHeader class="flex flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3">
        <div class="grid min-w-0 gap-1.5">
          <CardTitle>This {{ serverWhat }}'s own variables</CardTitle>
          <CardDescription>{{ serverBlurb }}</CardDescription>
        </div>
        <Button variant="outline" @click="showServer = true"><ChevronDown /> Show {{ list.length || '' }} {{ list.length === 1 ? 'setting' : 'settings' }}</Button>
      </CardHeader>
    </Card>
    <Card v-else>
      <CardHeader class="flex flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3">
        <div class="grid min-w-0 gap-1.5">
          <CardTitle>{{ service ? 'Variables' : serverApp ? `This ${serverWhat}'s own variables` : 'Environment variables' }}</CardTitle>
          <CardDescription v-if="serverApp">{{ serverBlurb }}</CardDescription>
          <CardDescription v-else-if="scope">
            Only <span class="font-mono">{{ scope }}</span> gets these. One with the same name as a shared variable wins over it. A change applies after a restart.
          </CardDescription>
          <CardDescription v-else-if="multi">
            Shared variables: every service of this app gets them. Tick Hidden for a password or key: its value is never shown again. A change applies after a restart, or on the
            next deploy when the build reads it.
          </CardDescription>
          <CardDescription v-else-if="site">
            Settings your site needs while it is built, like VITE_API_URL. Anyone can read a VITE_ or NEXT_PUBLIC_ value in the site, so never put a password in one. A change
            applies on the next deploy.
          </CardDescription>
          <CardDescription v-else>
            Passwords, API keys and other settings your app reads when it starts and while it is built. Tick Hidden for a password or key: its value is never shown again. A change
            applies after a restart, or on the next deploy when the build reads it.
          </CardDescription>
        </div>
        <div class="flex gap-2">
          <Button v-if="serverApp" variant="ghost" @click="showServer = false"><ChevronUp /> Hide</Button>
          <Button variant="outline" @click="openPaste"><ClipboardPaste /> Paste .env</Button>
        </div>
      </CardHeader>
      <CardContent class="grid gap-4">
        <!-- Whose variables: shared, or one service's own. -->
        <div v-if="multi && service === undefined" class="flex flex-wrap gap-1.5" role="group" aria-label="Whose variables">
          <button
            v-for="o in scopes"
            :key="o.value"
            type="button"
            :aria-pressed="scope === o.value"
            class="inline-flex h-8 items-center gap-1.5 rounded-md border px-2.5 text-xs text-muted-foreground transition-colors hover:bg-accent aria-pressed:border-primary/40 aria-pressed:bg-primary/10 aria-pressed:text-foreground"
            @click="pickedScope = o.value"
          >
            <span :class="o.value && 'font-mono'" class="font-medium text-foreground">{{ o.label }}</span>
            <span>{{ o.sub }}</span>
            <span v-if="o.count" class="rounded bg-muted px-1 tabular-nums">{{ o.count }}</span>
          </button>
        </div>

        <FormScope :form="addForm">
          <form id="env-add" class="grid gap-3 rounded-lg border bg-muted/30 p-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)_auto] sm:items-start" novalidate @submit.prevent="submitAdd">
            <FormField v-slot="{ componentField }" name="name">
              <FormItem>
                <FormLabel>Name</FormLabel>
                <FormControl><Input placeholder="API_KEY" autocapitalize="off" autocomplete="off" spellcheck="false" class="font-mono" v-bind="componentField" /></FormControl>
                <FormMessage />
              </FormItem>
            </FormField>
            <FormField v-slot="{ componentField }" name="value">
              <FormItem>
                <FormLabel>Value</FormLabel>
                <FormControl>
                  <Textarea
                    rows="1"
                    placeholder="Paste the value"
                    autocomplete="off"
                    spellcheck="false"
                    class="min-h-8 resize-y py-[5px] leading-5 font-mono"
                    v-bind="componentField"
                    @keydown="onAddKey"
                  />
                </FormControl>
                <FormMessage />
                <label class="flex w-fit items-center gap-2 text-xs text-muted-foreground" title="The value is never shown again, not even to you">
                  <Checkbox :model-value="addHidden" @update:model-value="(v) => (addHiddenPicked = v === true)" />
                  {{ addHidden ? 'Hidden: the value is never shown again' : 'Hidden' }}
                </label>
              </FormItem>
            </FormField>
            <Button type="submit" class="max-sm:order-last sm:mt-[1.375rem]" :disabled="save.isPending.value && !editing">
              <Plus /> {{ addingExisting ? 'Replace' : 'Add' }}
            </Button>
            <div v-if="suggestions.length && !scope && !app.data.value?.template" class="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground sm:col-span-3">
              <span>Use another app of {{ project?.name }}:</span>
              <button
                v-for="sg in suggestions"
                :key="sg.app.id"
                type="button"
                class="rounded-md border bg-card px-1.5 py-0.5 font-mono text-[11px] text-foreground/85 transition-colors hover:border-foreground/30"
                :title="`Fills in ${sg.name}=${sg.value}`"
                @click="pickSuggestion(sg)"
              >
{{ sg.name }} from {{ aliasOf(sg.app) }}
</button>
            </div>
            <p v-if="addingExisting" class="text-xs text-muted-foreground sm:col-span-3">
              <span class="font-mono">{{ addForm.values.name?.trim() }}</span> already exists. Saving replaces its value.
            </p>
          </form>
        </FormScope>

        <QueryError v-if="env.isError.value" :error="env.error.value" @retry="env.refetch()" />
        <Skeleton v-else-if="env.isPending.value" class="h-24 w-full" />
        <EmptyState
          v-else-if="!list.length && !inherited.length"
          :icon="KeyRound"
          :title="scope ? `${scope} has no variables of its own.` : multi ? 'No shared variables yet.' : 'No variables yet.'"
          :description="scope ? 'Add one above that only this service needs, or paste a .env file.' : `Add one above, like ${site ? 'VITE_API_URL' : 'DATABASE_URL'}, or paste the contents of a .env file.`"
        />
        <template v-else>
          <ListSearch v-if="list.length + inherited.length > 6" v-model="search" class="sm:max-w-xs" placeholder="Find a variable" label="Find a variable" />
          <p v-if="!shown.length && list.length" class="flex items-center gap-2 py-6 text-sm text-muted-foreground"><SearchX class="size-4" /> No variable is called “{{ search.trim() }}”.</p>
          <p v-else-if="!list.length" class="text-sm text-muted-foreground">No variables of its own yet.</p>
          <ul v-else class="divide-y rounded-lg border">
            <li v-for="v in shown" :key="v.name" class="px-3 py-2">
              <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
                <span class="font-mono text-sm font-medium break-all">{{ v.name }}</span>
                <span v-if="valueParts(v)" class="inline-flex min-w-0 flex-wrap items-center gap-0.5 font-mono text-sm text-muted-foreground">
                  <template v-for="(part, i) in valueParts(v)!" :key="i">
                    <RouterLink
                      v-if="'ref' in part && byAlias.get(part.ref.app)"
                      :to="appRoute(byAlias.get(part.ref.app)!)"
                      class="inline-flex items-center gap-1 rounded-md border border-primary/30 bg-primary/5 px-1.5 py-0.5 text-xs text-foreground hover:border-primary/60"
                      :title="`${part.ref.variable} of ${part.ref.app}, kept up to date by bitploy`"
                    ><span class="text-primary">{{ part.ref.app }}</span><span class="text-muted-foreground">.</span>{{ part.ref.variable }}</RouterLink>
                    <span v-else-if="'ref' in part" class="rounded-md border border-dashed px-1.5 py-0.5 text-xs" :title="`No app called ${part.ref.app} in this project`">{{ part.ref.raw }}</span>
                    <span v-else-if="part.text.includes('•')" class="tracking-tight" title="The rest of the value is hidden">{{ part.text }}</span>
                    <span v-else>{{ part.text }}</span>
                  </template>
                </span>
                <span v-else-if="v.value !== undefined" class="min-w-0 max-w-full truncate font-mono text-sm text-muted-foreground sm:max-w-md" :title="v.value">{{ v.value }}</span>
                <span v-else class="inline-flex items-center gap-1 font-mono text-sm text-muted-foreground" title="Hidden: the value is never shown again">
                  {{ v.masked }}<Lock class="size-3" />
                </span>
                <span class="text-xs text-muted-foreground">changed <RelativeTime :date="v.updated_at" /></span>
                <div v-if="editing !== v.name" class="ml-auto flex gap-1">
                  <Button size="sm" variant="ghost" :aria-label="`Change ${v.name}`" @click="startEdit(v)"><Pencil /> Change</Button>
                  <Button size="sm" variant="ghost" class="text-muted-foreground hover:text-destructive" :aria-label="`Remove ${v.name}`" @click="toRemove = v.name"><Trash2 /> Remove</Button>
                </div>
              </div>
              <form v-if="editing === v.name" class="mt-2 grid gap-2" novalidate @submit.prevent="saveEdit">
                <Label :for="`env-edit-${v.name}`" class="sr-only">Value of {{ v.name }}</Label>
                <Textarea
                  :id="`env-edit-${v.name}`"
                  v-model="editValue"
                  rows="1"
                  :placeholder="v.hidden ? 'The new value' : 'The value'"
                  autocomplete="off"
                  spellcheck="false"
                  class="min-h-8 resize-y py-[5px] leading-5 font-mono"
                  @keydown="onEditKey"
                />
                <div class="flex flex-wrap items-center gap-2">
                  <Button type="submit" size="sm" :disabled="save.isPending.value || editNeedsValue">{{ save.isPending.value ? 'Saving...' : 'Save' }}</Button>
                  <Button type="button" size="sm" variant="ghost" :disabled="save.isPending.value" @click="cancelEdit">Cancel</Button>
                  <label class="flex items-center gap-2 text-xs text-muted-foreground" title="The value is never shown again, not even to you">
                    <Checkbox v-model="editHidden" />
                    Hidden
                  </label>
                  <span class="text-xs text-muted-foreground">{{
                    editNeedsValue
                      ? 'Enter the value again to make it readable.'
                      : editHidden && !v.hidden
                        ? 'Saved hidden, the value is never shown again. Enter saves, Esc cancels.'
                        : v.hidden
                          ? 'The old value is not shown; leave the box empty to keep it. Enter saves, Esc cancels.'
                          : 'Enter saves, Esc cancels.'
                  }}</span>
                </div>
              </form>
            </li>
          </ul>
          <div v-if="shownInherited.length" class="grid gap-2">
            <p class="text-xs text-muted-foreground">Also gets {{ shownInherited.length === 1 ? 'this shared variable' : `these ${shownInherited.length} shared variables` }}. Change them under Shared.</p>
            <ul class="divide-y rounded-lg border border-dashed">
              <li v-for="v in shownInherited" :key="v.name" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2" :class="names.has(v.name) && 'opacity-60'">
                <span class="font-mono text-sm break-all" :class="names.has(v.name) && 'line-through'">{{ v.name }}</span>
                <span class="min-w-0 max-w-full truncate font-mono text-sm text-muted-foreground sm:max-w-md" :title="v.value">{{ v.value ?? v.masked }}</span>
                <Badge variant="outline" class="ml-auto font-normal">{{ names.has(v.name) ? 'replaced by its own' : 'shared' }}</Badge>
              </li>
            </ul>
          </div>
        </template>
      </CardContent>
    </Card>

    <Dialog v-model:open="pasteOpen">
      <DialogContent class="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Paste a .env file</DialogTitle>
          <DialogDescription>One NAME=value per line.<template v-if="multi"> They are saved {{ scope ? `for ${scope} only` : 'for every service' }}.</template></DialogDescription>
        </DialogHeader>
        <Label for="env-paste" class="sr-only">Contents of the .env file</Label>
        <Textarea id="env-paste" v-model="pasteText" rows="8" placeholder="DATABASE_URL=...&#10;API_KEY=..." spellcheck="false" autocomplete="off" class="max-h-72 font-mono text-xs" />
        <div v-if="pasteText.trim()" class="grid gap-2 text-sm">
          <p v-if="parsed.vars.length">
            {{ parsed.vars.length }} {{ parsed.vars.length === 1 ? 'variable' : 'variables' }}<template v-if="replacing">, {{ replacing }} of them replace{{ replacing === 1 ? 's' : '' }} a value you have</template>.
          </p>
          <ul v-if="parsed.vars.length" class="flex max-h-28 flex-wrap gap-1.5 overflow-y-auto">
            <li v-for="v in parsed.vars" :key="v.name">
              <Badge
                :variant="names.has(v.name) ? 'outline' : 'secondary'"
                class="font-mono"
                :title="(names.has(v.name) ? 'Replaces the value you have' : 'New') + (pasteHidden[v.name] ? ', hidden' : '')"
              >
                {{ v.name }}<Lock v-if="pasteHidden[v.name]" />
              </Badge>
            </li>
          </ul>
          <p v-if="pasteHiddenCount" class="text-xs text-muted-foreground">
            <Lock class="inline size-3" /> Saved hidden: names with SECRET, TOKEN, KEY or PASSWORD, and the ones hidden already. Change one afterwards in its row.
          </p>
          <p v-if="parsed.skipped.length" class="text-xs text-destructive">
            Not understood, left out: line {{ parsed.skipped.map((s) => s.line).join(', ') }}.
          </p>
        </div>
        <DialogFooter>
          <Button variant="ghost" :disabled="importAll.isPending.value" @click="pasteOpen = false">Cancel</Button>
          <Button :disabled="!parsed.vars.length || importAll.isPending.value" @click="importAll.mutate()">
            {{ importAll.isPending.value ? 'Saving...' : parsed.vars.length ? `Save ${parsed.vars.length} ${parsed.vars.length === 1 ? 'variable' : 'variables'}` : 'Save' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      v-model:open="removeOpen"
      :title="`Remove ${toRemove ?? 'this variable'}?`"
      :description="`${toRemove ?? 'The variable'} is removed${forWhom} the next time the app starts. An app that reads it may stop working.`"
      confirm-label="Remove"
      :loading="remove.isPending.value"
      @confirm="toRemove && remove.mutate(toRemove)"
    />
  </div>
</template>
