<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { Cable } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import { keys, useProject, useProjectEnv } from '@/composables/queries'
import { ApiError, api } from '@/lib/api'
import { appPath, appRoute, projectRoute } from '@/lib/appLinks'
import { toastError, toastLoading } from '@/lib/notify'
import { markRestartNeeded } from '@/composables/useRestartNeeded'
import { aliasOf, dependentsFromError, dependentsOf, projectIdOf, projectLinks } from '@/lib/projects'
import type { Dependent } from '@/lib/projects'
import type { App } from '@/lib/types'

// Deleting an app. When other apps of the project read its variables, the
// dialog lists them (with the variables): the server refuses the delete with
// app_in_use while they are there. The dialog offers to remove those variables
// first and then delete, or to open each app's variables and change them.
const props = defineProps<{ app: App }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ deleted: [] }>()
const route = useRoute()
const router = useRouter()
const qc = useQueryClient()

const pid = computed(() => projectIdOf(props.app))
const { list, project } = useProject(pid)
const apps = computed(() => project.value?.apps ?? [props.app])
const env = useProjectEnv(apps)
const known = computed(() => dependentsOf(props.app, apps.value, projectLinks(apps.value, env.value)))
// What the server said when it refused, which wins over what the page worked out.
const refused = ref<Dependent[] | null>(null)
watch(open, (o) => !o && (refused.value = null))
const dependents = computed(() => refused.value ?? known.value)
const inUse = computed(() => dependents.value.length > 0)

// Removes the variables of one app that read from this one: per service, since
// a compose app can have the same name in several services. Only the ones that
// refer to this app go; a variable of the same name that does not stays.
async function unsetVars(d: Dependent) {
  const prefix = `\${{${aliasOf(props.app)}.`
  const byService = new Map<string, string[]>()
  for (const v of await api.env(d.slug)) {
    const reads = v.references ? v.references.some((r) => r.startsWith(prefix)) : d.vars.includes(v.name)
    if (reads) byService.set(v.service, [...(byService.get(v.service) ?? []), v.name])
  }
  for (const [service, names] of byService) await api.setEnv(d.slug, {}, names, service || undefined)
  const a = apps.value.find((x) => x.slug === d.slug)
  markRestartNeeded(a, 'variables')
  void qc.invalidateQueries({ queryKey: keys.env(d.slug) })
}

function linkTo(d: Dependent) {
  const a = apps.value.find((x) => x.slug === d.slug)
  return a ? appRoute(a, 'app-variables') : appPath(d.slug, 'variables')
}

// The dialog closes at once and a loading toast follows the delete. When the
// server refuses because other apps read its variables, the dialog opens again
// with them listed (or, when its panel is gone by then, the toast says why).
let mounted = true
onBeforeUnmount(() => (mounted = false))
const del = useMutation({
  onMutate: () => {
    open.value = false
    return toastLoading(`Deleting ${props.app.name}...`)
  },
  // The list as the dialog showed it: closing the dialog clears what the server said.
  mutationFn: async (changed: Dependent[]) => {
    for (const d of changed) await unsetVars(d)
    await api.deleteApp(props.app.slug)
    return changed
  },
  onSuccess: async (changed, _v, t) => {
    const slug = props.app.slug
    t.ok(
      `${props.app.name} deleted`,
      changed.length ? `Removed its variables from ${changed.map((d) => d.alias).join(', ')}. Restart ${changed.length === 1 ? 'that app' : 'those apps'} to stop using them.` : undefined,
    )
    emit('deleted')
    // Leave the app's own page first: removing its cache while the page polls would flash a 404.
    const rest = project.value?.apps.filter((a) => a.slug !== slug) ?? []
    if (route.params.slug === slug) await router.replace(!list.data.value?.fallback && rest.length ? projectRoute(pid.value) : '/')
    else if (route.query.app === slug) await router.replace({ query: { ...route.query, app: undefined } })
    for (const key of [keys.app(slug), keys.deployments(slug), keys.env(slug), keys.domains(slug)]) qc.removeQueries({ queryKey: key })
    qc.removeQueries({ queryKey: ['metrics', slug] })
    void qc.invalidateQueries({ queryKey: keys.apps })
    void qc.invalidateQueries({ queryKey: keys.me })
  },
  onError: (e, _v, t) => {
    if (e instanceof ApiError && e.code === 'app_in_use') {
      const listed = dependentsFromError(e.details)
      const list = listed.length ? listed : known.value
      // The page's own list was behind; fetch the variables again.
      for (const a of apps.value) void qc.invalidateQueries({ queryKey: keys.env(a.slug) })
      if (list.length && mounted) {
        t?.dismiss()
        refused.value = list
        open.value = true
        return
      }
    }
    if (t) t.fail(e)
    else toastError(e)
  },
})

const description = computed(() => {
  const n = dependents.value.length
  const gone = props.app.template ? 'its data and its backups' : 'its saved files and its backups'
  if (!n) return `Everything about ${props.app.name} is removed, including ${gone}. This cannot be undone.`
  return `${n === 1 ? 'Another app reads' : `${n} apps read`} its variables, listed below. Deleting removes those variables from ${n === 1 ? 'that app' : 'them'} and then ${props.app.name} with ${gone}; ${n === 1 ? 'it stops' : 'they stop'} reaching ${props.app.name}. To point ${n === 1 ? 'it' : 'them'} somewhere else instead, open ${n === 1 ? 'its' : 'their'} variables. This cannot be undone.`
})
</script>

<template>
  <ConfirmDialog
    v-model:open="open"
    :title="inUse ? `${app.name} is in use` : `Delete ${app.name} for good?`"
    :description="description"
    :confirm-label="inUse ? 'Remove these variables and delete' : 'Delete app'"
    :type-to-confirm="app.slug"
    :loading="del.isPending.value"
    @confirm="del.mutate(dependents)"
  >
    <ul v-if="inUse" class="divide-y rounded-lg border text-sm" data-testid="app-dependents">
      <li v-for="d in dependents" :key="d.slug" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
        <Cable class="size-3.5 shrink-0 text-muted-foreground" />
        <span class="font-mono font-medium">{{ d.alias }}</span>
        <span class="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground">{{ d.vars.length ? d.vars.join(', ') : 'reads its variables' }}</span>
        <RouterLink :to="linkTo(d)" class="text-xs font-medium underline-offset-4 hover:underline" @click="open = false">Open its variables</RouterLink>
      </li>
    </ul>
  </ConfirmDialog>
</template>
