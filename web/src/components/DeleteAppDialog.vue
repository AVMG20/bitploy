<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { Cable } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import { keys, useProject, useProjectEnv } from '@/composables/queries'
import { ApiError, api } from '@/lib/api'
import { appPath, appRoute, projectRoute } from '@/lib/appLinks'
import { toastError, toastOk } from '@/lib/notify'
import { dependentsFromError, dependentsOf, projectIdOf, projectLinks } from '@/lib/projects'
import type { Dependent } from '@/lib/projects'
import type { App } from '@/lib/types'

// Deleting an app. When other apps of the project read its variables, the
// dialog lists them (with the variables) and the delete waits until they are
// removed: the server refuses with app_in_use, so nobody has to try first.
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
watch(open, (o) => o && (refused.value = null))
const dependents = computed(() => refused.value ?? known.value)
const blocked = computed(() => dependents.value.length > 0)

function linkTo(d: Dependent) {
  const a = apps.value.find((x) => x.slug === d.slug)
  return a ? appRoute(a, 'app-variables') : appPath(d.slug, 'variables')
}

const del = useMutation({
  mutationFn: () => api.deleteApp(props.app.slug),
  onSuccess: async () => {
    const slug = props.app.slug
    toastOk(`${props.app.name} deleted`)
    open.value = false
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
  onError: (e) => {
    if (e instanceof ApiError && e.code === 'app_in_use') {
      const listed = dependentsFromError(e.details)
      refused.value = listed.length ? listed : known.value
      // The page's own list was behind; fetch the variables again.
      for (const a of apps.value) void qc.invalidateQueries({ queryKey: keys.env(a.slug) })
      if (refused.value.length) return
    }
    toastError(e)
  },
})

const description = computed(() => {
  const n = dependents.value.length
  if (!n) return `Everything about ${props.app.name} is removed, including its saved files and its backups. This cannot be undone.`
  return `${n === 1 ? 'Another app reads' : `${n} apps read`} its variables and would stop working. Remove those variables from ${n === 1 ? 'it' : 'them'} first, then delete ${props.app.name}.`
})
</script>

<template>
  <ConfirmDialog
    v-model:open="open"
    :title="blocked ? `${app.name} is in use` : `Delete ${app.name} for good?`"
    :description="description"
    confirm-label="Delete app"
    :type-to-confirm="app.slug"
    :blocked="blocked"
    :loading="del.isPending.value"
    @confirm="del.mutate()"
  >
    <ul v-if="blocked" class="divide-y rounded-lg border text-sm" data-testid="app-dependents">
      <li v-for="d in dependents" :key="d.slug" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
        <Cable class="size-3.5 shrink-0 text-muted-foreground" />
        <span class="font-mono font-medium">{{ d.alias }}</span>
        <span class="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground">{{ d.vars.length ? d.vars.join(', ') : 'reads its variables' }}</span>
        <RouterLink :to="linkTo(d)" class="text-xs font-medium underline-offset-4 hover:underline" @click="open = false">Open its variables</RouterLink>
      </li>
    </ul>
  </ConfirmDialog>
</template>
