<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useQueryClient } from '@tanstack/vue-query'
import { ArrowLeft, Check, Info, Loader2, Lock, Plus, X } from 'lucide-vue-next'
import ServiceIcon from '@/components/ServiceIcon.vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import { keys, useApp, useMe, useProjectEnv, useProjects } from '@/composables/queries'
import { api } from '@/lib/api'
import { appRoute } from '@/lib/appLinks'
import { friendlyError } from '@/lib/errors'
import type { FriendlyError } from '@/lib/errors'
import { formatMB, pct } from '@/lib/format'
import { aliasOf, appRole, connectable, defaultConnect, freeAlias, roleTitle, sharedVar, templateRole, varSource } from '@/lib/projects'
import { memoryLeftMB, NO_OWN_APPS, noOwnApps, storageLeftMB } from '@/lib/quota'
import { listWords } from '@/lib/services'
import type { RoleKey } from '@/lib/services'
import type { App, ServiceTemplate } from '@/lib/types'
import { cn } from '@/lib/utils'

// Add a database (or another ready service) to a project: its short name,
// which apps get its connection variable, what it takes of your allowance, then live
// progress until it runs. Without a fixed project (the Ready-made page) it
// first asks which project, or makes a new one.
const props = defineProps<{ template: ServiceTemplate; projectId?: string; showBack?: boolean }>()
const emit = defineEmits<{ back: []; done: [] }>()
const qc = useQueryClient()
const me = useMe()
const projects = useProjects()

// ---------------------------------------------------------------- which project
const NEW = '__new__'
const chosen = ref(props.projectId ?? '')
const newName = ref('')
// Newest first, as in the sidebar.
const list = computed(() => [...(projects.data.value?.projects ?? [])].sort((a, b) => b.created_at.localeCompare(a.created_at)))
watch(
  list,
  (l) => {
    if (!chosen.value) chosen.value = l[0]?.id ?? NEW
  },
  { immediate: true },
)
const project = computed(() => list.value.find((p) => p.id === chosen.value) ?? null)
const isNew = computed(() => chosen.value === NEW)

// ---------------------------------------------------------------- name and connections
const taken = computed(() => (project.value?.apps ?? []).map(aliasOf))
const name = ref('')
watch(
  [() => props.template.id, project],
  () => (name.value = freeAlias(props.template.id, taken.value)),
  { immediate: true },
)
// The template's own name is taken: say why it is called postgres-2.
const renamed = computed(() => taken.value.includes(props.template.id) && name.value.trim() !== props.template.id && !nameError.value)
const nameError = computed(() => {
  const n = name.value.trim()
  if (!n) return 'Give it a short name.'
  if (!/^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$/.test(n)) return 'Use 1 to 32 lowercase letters, numbers and dashes, like postgres or db-2.'
  if (taken.value.includes(n)) return `${n} is taken in this project.`
  return ''
})

const candidates = computed(() => connectable(project.value?.apps ?? []))
const mainVar = computed(() => props.template.main_var || props.template.connection_vars[0] || '')
// An app that already has the variable (web has DATABASE_URL from postgres) is
// not ticked and cannot be: connecting would replace its working connection, and
// the server refuses it. Its variables say where it points now.
const env = useProjectEnv(candidates)
const envReady = computed(() => candidates.value.every((a) => env.value[a.slug] !== undefined))
const hasVar = (slug: string) => !!sharedVar(env.value[slug], mainVar.value)
const blocked = computed(() => candidates.value.filter((a) => hasVar(a.slug)).map((a) => a.slug))
const connect = ref<string[]>([])
const touched = ref(false)
watch(project, () => (touched.value = false))
watch(
  [project, envReady, blocked],
  ([p, ready]) => {
    if (touched.value) connect.value = connect.value.filter((s) => !blocked.value.includes(s))
    // Wait for the variables before ticking anything, so an app that has the variable is never ticked.
    else connect.value = p && ready ? defaultConnect(p, blocked.value) : []
  },
  { immediate: true },
)
function toggle(slug: string, on: boolean | 'indeterminate') {
  touched.value = true
  connect.value = on === true ? [...new Set([...connect.value, slug])] : connect.value.filter((s) => s !== slug)
}
function usesNow(slug: string): string {
  const from = varSource(sharedVar(env.value[slug], mainVar.value))
  return from ? `Already has ${mainVar.value}, from ${from}.` : `Already has its own ${mainVar.value}.`
}
const role = computed<RoleKey>(() => templateRole(props.template))

// ---------------------------------------------------------------- does it fit
// A project shared with you counts against its owner's allowance, which the
// server checks; only your own projects are checked here.
const shared = computed(() => !isNew.value && !!project.value?.shared)
const own = computed(() => (shared.value ? null : me.data.value))
const memLeft = computed(() => memoryLeftMB(own.value))
const storageLeft = computed(() => storageLeftMB(own.value))
const backedUp = computed(() => templateRole(props.template) === 'database')
const memOver = computed(() => memLeft.value !== null && props.template.memory_mb > memLeft.value)
const storageOver = computed(() => storageLeft.value !== null && props.template.volume_mb > storageLeft.value)
const noOwn = computed(() => noOwnApps(own.value))
const appsFull = computed(() => !!own.value && own.value.usage.apps >= own.value.quota.apps)

// ---------------------------------------------------------------- add, then follow it
const stage = ref<'form' | 'adding' | 'progress'>('form')
const error = ref<FriendlyError | null>(null)
const added = ref<App | null>(null)
const connected = ref<string[]>([])
const live = useApp(() => added.value?.slug ?? '', () => !!added.value)
const now = computed(() => live.data.value ?? added.value)
const state = computed<'starting' | 'running' | 'failed'>(() => {
  const s = now.value?.status
  if (s === 'running') return 'running'
  if (s === 'failed' || s === 'crashed') return 'failed'
  return 'starting'
})
const failure = computed(() => now.value?.current_deployment?.error ?? null)

async function add() {
  if (nameError.value && !isNew.value) return
  error.value = null
  stage.value = 'adding'
  try {
    let pid = chosen.value
    if (isNew.value) {
      const p = await api.createProject({ name: newName.value.trim() || props.template.name })
      pid = p.id
    }
    const slugs = isNew.value ? [] : connect.value
    const app = await api.addService(pid, { template: props.template.id, name: name.value.trim(), connect_to: slugs.length ? slugs : undefined })
    added.value = app
    connected.value = slugs
    qc.setQueryData(keys.app(app.slug), app)
    void qc.invalidateQueries({ queryKey: keys.apps })
    void qc.invalidateQueries({ queryKey: keys.me })
    for (const s of slugs) void qc.invalidateQueries({ queryKey: keys.env(s) })
    stage.value = 'progress'
  } catch (e) {
    error.value = friendlyError(e)
    stage.value = 'form'
  }
}
const connectedNames = computed(() => (project.value?.apps ?? []).filter((a) => connected.value.includes(a.slug)).map((a) => a.name))
const busy = computed(() => stage.value === 'adding')
</script>

<template>
  <div class="grid gap-5">
    <div class="flex items-start gap-3">
      <Button v-if="showBack && stage === 'form'" variant="ghost" size="icon" class="-ml-2 shrink-0" aria-label="Back to the list" @click="emit('back')"><ArrowLeft /></Button>
      <ServiceIcon :role="role" />
      <div class="min-w-0">
        <p class="font-medium">{{ template.name }} <span class="font-normal text-muted-foreground">{{ template.version }}</span></p>
        <p class="text-sm text-muted-foreground">{{ template.description }}</p>
      </div>
    </div>

    <template v-if="stage !== 'progress'">
      <div v-if="!projectId" class="grid gap-2">
        <Label>Add it to</Label>
        <div class="grid gap-1.5" role="radiogroup" aria-label="Project">
          <button
            v-for="p in list"
            :key="p.id"
            type="button"
            role="radio"
            :aria-checked="chosen === p.id"
            :disabled="busy"
            class="flex items-center gap-3 rounded-lg border px-3 py-2 text-left text-sm transition-colors hover:bg-accent/50 aria-checked:border-primary/50 aria-checked:bg-primary/5"
            @click="chosen = p.id"
          >
            <span class="flex size-3.5 items-center justify-center rounded-full border" :class="chosen === p.id && 'border-primary'" aria-hidden="true">
              <span v-if="chosen === p.id" class="size-1.5 rounded-full bg-primary" />
            </span>
            <span class="min-w-0 flex-1 truncate font-medium">{{ p.name }}</span>
            <span class="shrink-0 text-xs text-muted-foreground">{{ p.apps.length === 1 ? '1 app' : `${p.apps.length} apps` }}</span>
          </button>
          <button
            type="button"
            role="radio"
            :aria-checked="isNew"
            :disabled="busy"
            class="flex items-center gap-3 rounded-lg border border-dashed px-3 py-2 text-left text-sm transition-colors hover:bg-accent/50 aria-checked:border-primary/50 aria-checked:bg-primary/5"
            @click="chosen = NEW"
          >
            <span class="flex size-3.5 items-center justify-center rounded-full border" :class="isNew && 'border-primary'" aria-hidden="true">
              <span v-if="isNew" class="size-1.5 rounded-full bg-primary" />
            </span>
            <Plus class="size-3.5 text-muted-foreground" /> <span class="font-medium">A new project</span>
          </button>
          <div v-if="isNew" class="grid gap-1.5 pl-6">
            <Label for="tpl-project-name" class="text-xs text-muted-foreground">Name of the new project</Label>
            <Input id="tpl-project-name" v-model="newName" :placeholder="template.name" autocomplete="off" :disabled="busy" />
          </div>
        </div>
      </div>

      <div class="grid gap-1.5">
        <Label for="tpl-name">Short name</Label>
        <Input id="tpl-name" v-model="name" class="font-mono" autocapitalize="off" autocomplete="off" spellcheck="false" :disabled="busy" :aria-invalid="!!nameError || undefined" />
        <p v-if="nameError" class="text-[0.8rem] text-destructive">{{ nameError }}</p>
        <template v-else>
          <p v-if="renamed" class="text-[0.8rem] text-foreground/85" data-testid="tpl-renamed">
            <Info class="mr-1 inline size-3 align-[-1px]" />Named <span class="font-mono">{{ name.trim() }}</span> because {{ project?.name }} already has an app called <span class="font-mono">{{ template.id }}</span>.
          </p>
          <p class="text-[0.8rem] text-muted-foreground">
            <Lock class="mr-1 inline size-3 align-[-1px]" />Private. The other apps of {{ isNew ? 'the project' : project?.name }} reach it at
            <span class="font-mono whitespace-nowrap text-foreground/80">{{ name.trim() }}:{{ template.port }}</span>; the internet cannot.
          </p>
        </template>
      </div>

      <div v-if="!isNew && candidates.length && mainVar" class="grid gap-2">
        <Label>Connect to</Label>
        <div class="grid gap-1.5">
          <label
            v-for="a in candidates"
            :key="a.slug"
            :class="cn(
              'flex items-center gap-3 rounded-lg border px-3 py-2 text-sm transition-colors has-[[data-state=checked]]:border-primary/40',
              hasVar(a.slug) ? 'bg-muted/30' : 'cursor-pointer hover:bg-accent/40',
            )"
          >
            <Checkbox :model-value="connect.includes(a.slug)" :disabled="busy || hasVar(a.slug)" @update:model-value="(v) => toggle(a.slug, v)" />
            <span class="min-w-0 flex-1">
              <span class="font-medium">{{ a.name }}</span>
              <span class="ml-2 text-xs text-muted-foreground">{{ roleTitle(appRole(a)) }}</span>
              <span v-if="hasVar(a.slug)" class="block text-xs text-muted-foreground" data-testid="tpl-has-var">
                {{ usesNow(a.slug) }} It keeps using that. To switch it to <span class="font-mono">{{ name.trim() || template.id }}</span>, change {{ mainVar }} in its Variables after adding.
              </span>
            </span>
            <span v-if="aliasOf(a) !== a.name" class="hidden truncate font-mono text-xs text-muted-foreground sm:inline">{{ aliasOf(a) }}</span>
          </label>
        </div>
        <p class="text-[0.8rem] text-muted-foreground">
          Each ticked app gets a variable <span class="font-mono text-foreground/80">{{ mainVar }}</span> with the address and password of
          <span class="font-mono text-foreground/80">{{ name.trim() || template.id }}</span>, and restarts to use it.
        </p>
      </div>

      <div class="grid gap-2 rounded-lg border bg-muted/30 p-3">
        <div class="flex items-baseline justify-between gap-3 text-xs">
          <span class="text-muted-foreground">Memory it can use</span>
          <span class="tabular-nums" :class="memOver && 'font-medium text-destructive'">
            {{ formatMB(template.memory_mb) }}<span v-if="memLeft !== null" class="text-muted-foreground"> of {{ formatMB(memLeft) }} left</span>
          </span>
        </div>
        <Progress :class="cn(memOver ? '[&>[data-slot=progress-indicator]]:bg-destructive' : '[&>[data-slot=progress-indicator]]:bg-success')" :model-value="memLeft ? Math.min(100, pct(template.memory_mb, memLeft)) : 100" />
        <div v-if="template.volume_mb" class="flex items-baseline justify-between gap-3 text-xs">
          <span class="text-muted-foreground">{{ backedUp ? 'Storage for its data, backed up' : 'Storage for its data' }}</span>
          <span class="tabular-nums" :class="storageOver && 'font-medium text-destructive'">
            {{ formatMB(template.volume_mb) }}<span v-if="storageLeft !== null" class="text-muted-foreground"> of {{ formatMB(storageLeft) }} left</span>
          </span>
        </div>
        <p v-if="shared" class="text-xs text-muted-foreground">It counts against the limits of the project's owner.</p>
        <p v-if="noOwn" class="text-xs text-destructive">{{ NO_OWN_APPS }}</p>
        <p v-else-if="memOver || storageOver" class="text-xs text-destructive">This does not fit in what is left of your allowance. Stop or delete an app first, or ask your admin for more.</p>
        <p v-else-if="appsFull" class="text-xs text-destructive">You have {{ me.data.value?.quota.apps }} apps, the most your account allows. Delete one first, or ask your admin for more.</p>
      </div>

      <div v-if="error" class="rounded-lg border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm">
        <p class="font-medium text-destructive">{{ error.message }}</p>
        <p v-if="error.hint" class="text-muted-foreground">{{ error.hint }}</p>
      </div>

      <div class="flex flex-wrap items-center justify-end gap-2">
        <Button v-if="showBack" variant="ghost" :disabled="busy" @click="emit('back')">Back</Button>
        <Button :disabled="busy || (!isNew && !!nameError) || memOver || storageOver || appsFull || noOwn" @click="add">
          <Loader2 v-if="busy" class="animate-spin" />
          {{ busy ? `Adding ${template.name}...` : `Add ${template.name}` }}
        </Button>
      </div>
    </template>

    <!-- Live progress: created, started, wired. -->
    <template v-else-if="added">
      <ol class="grid gap-3" aria-live="polite">
        <li class="flex items-start gap-3 text-sm">
          <span class="mt-0.5 flex size-5 items-center justify-center rounded-full bg-success/15 text-success"><Check class="size-3.5" /></span>
          <span>Created <span class="font-mono">{{ aliasOf(added) }}</span> in {{ added.project?.name ?? project?.name ?? 'the project' }}, with its own password.</span>
        </li>
        <li class="flex items-start gap-3 text-sm">
          <span
            :class="cn('mt-0.5 flex size-5 items-center justify-center rounded-full', state === 'running' ? 'bg-success/15 text-success' : state === 'failed' ? 'bg-destructive/15 text-destructive' : 'bg-info/15 text-info')"
          >
            <Check v-if="state === 'running'" class="size-3.5" />
            <X v-else-if="state === 'failed'" class="size-3.5" />
            <Loader2 v-else class="size-3.5 animate-spin" />
          </span>
          <span class="min-w-0">
            <template v-if="state === 'running'">{{ template.name }} is running at <span class="font-mono">{{ added.internal_host || `${aliasOf(added)}:${template.port}` }}</span>.</template>
            <template v-else-if="state === 'failed'">{{ template.name }} did not start.</template>
            <template v-else>Downloading and starting {{ template.name }} {{ template.version }}. This takes up to a minute.</template>
            <span v-if="state === 'failed' && failure" class="block text-muted-foreground">{{ failure.message }} {{ failure.hint }}</span>
          </span>
        </li>
        <li v-if="connected.length" class="flex items-start gap-3 text-sm">
          <span :class="cn('mt-0.5 flex size-5 items-center justify-center rounded-full', state === 'running' ? 'bg-success/15 text-success' : 'bg-muted text-muted-foreground')">
            <Check v-if="state === 'running'" class="size-3.5" />
            <span v-else class="size-1.5 rounded-full bg-current" />
          </span>
          <span>{{ listWords(connectedNames) }} {{ connected.length === 1 ? 'gets' : 'get' }} <span class="font-mono">{{ mainVar }}</span> and {{ connected.length === 1 ? 'restarts' : 'restart' }} to use it.</span>
        </li>
      </ol>
      <div class="flex flex-wrap items-center justify-end gap-2">
        <Button variant="outline" as-child @click="emit('done')">
          <RouterLink :to="appRoute(added, state === 'failed' ? 'app-logs' : 'app-variables')">{{ state === 'failed' ? 'Open its logs' : `See its variables` }}</RouterLink>
        </Button>
        <Button @click="emit('done')">Done</Button>
      </div>
    </template>
  </div>
</template>
