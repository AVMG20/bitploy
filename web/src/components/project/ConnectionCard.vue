<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { Cable, Lock, Plus } from 'lucide-vue-next'
import CopyButton from '@/components/CopyButton.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { keys, useProject, useProjectEnv, useServiceTemplates } from '@/composables/queries'
import { markRestartNeeded } from '@/composables/useRestartNeeded'
import { api } from '@/lib/api'
import { appRoute } from '@/lib/appLinks'
import { toastError, toastOk } from '@/lib/notify'
import { aliasOf, connectable, internalHostOf, projectIdOf, projectLinks, referenceText, usedBy } from '@/lib/projects'
import type { App } from '@/lib/types'

// How the other apps of the project use a database (or cache, or search
// engine): its connection variables, each copied as a ${{alias.VAR}} reference
// that stays right when the password changes, who uses it now, and a button to
// connect another app.
const props = defineProps<{ app: App }>()
const qc = useQueryClient()
const { list, project } = useProject(() => projectIdOf(props.app))
const templates = useServiceTemplates()
const template = computed(() => templates.data.value?.find((t) => t.id === props.app.template) ?? null)
const fallback = computed(() => !!list.data.value?.fallback)

const own = useQuery({ queryKey: computed(() => keys.env(props.app.slug)), queryFn: () => api.env(props.app.slug) })
const apps = computed(() => project.value?.apps ?? [props.app])
const env = useProjectEnv(apps)
const links = computed(() => projectLinks(apps.value, env.value))
const users = computed(() => usedBy(props.app, apps.value, links.value))
const others = computed(() => connectable(apps.value).filter((a) => a.id !== props.app.id && !users.value.some((u) => u.id === a.id)))

const alias = computed(() => aliasOf(props.app))
const mainVar = computed(() => template.value?.main_var || template.value?.connection_vars[0] || own.data.value?.[0]?.name || '')
// The variables to show: the template's connection variables, else whatever the app has.
const vars = computed(() => {
  const names = template.value?.connection_vars.length ? template.value.connection_vars : (own.data.value ?? []).map((v) => v.name)
  return names.map((name) => ({ name, masked: own.data.value?.find((v) => v.name === name)?.masked ?? '••••••••', main: name === mainVar.value }))
})

const connect = useMutation({
  mutationFn: (to: App) => api.setEnv(to.slug, { [mainVar.value]: referenceText(alias.value, mainVar.value) }, []),
  onSuccess: (_r, to) => {
    markRestartNeeded(to, 'variables')
    void qc.invalidateQueries({ queryKey: keys.env(to.slug) })
    toastOk(`${to.name} gets ${mainVar.value}`, to.status === 'running' ? `Restart ${to.name} to use it.` : `${to.name} uses it the next time it starts.`)
  },
  onError: (e) => toastError(e),
})
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle class="flex items-center gap-2"><Cable class="size-4 text-muted-foreground" /> Connect your apps</CardTitle>
      <CardDescription>
        <span class="inline-flex items-center gap-1"><Lock class="size-3" /> Private.</span>
        The other apps of {{ project?.name ?? 'the project' }} reach it at <span class="font-mono text-foreground/80">{{ internalHostOf(app) }}</span>. Copy a reference into an app's variables; it keeps working when the password changes.
      </CardDescription>
    </CardHeader>
    <CardContent class="grid gap-4">
      <ul class="divide-y rounded-lg border">
        <li v-for="v in vars" :key="v.name" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
          <span class="font-mono text-sm font-medium">{{ v.name }}</span>
          <span v-if="v.main" class="rounded bg-primary/10 px-1.5 py-0.5 text-[11px] text-primary">most apps need this one</span>
          <span class="font-mono text-sm text-muted-foreground">{{ v.masked }}</span>
          <span class="ml-auto flex items-center gap-1">
            <span class="hidden font-mono text-xs text-muted-foreground md:inline">{{ referenceText(alias, v.name) }}</span>
            <CopyButton :value="referenceText(alias, v.name)" label="Copy reference" what="Reference copied. Paste it as the value of a variable in another app." />
          </span>
        </li>
      </ul>

      <div class="grid gap-2 text-sm">
        <p v-if="users.length" class="flex flex-wrap items-center gap-1.5">
          <span class="text-muted-foreground">Used by</span>
          <RouterLink v-for="u in users" :key="u.id" :to="appRoute(u, 'app-variables')" class="rounded-md border bg-muted/50 px-1.5 py-0.5 font-mono text-xs hover:border-foreground/30">{{ aliasOf(u) }}</RouterLink>
        </p>
        <p v-else class="text-muted-foreground">No app uses it yet.</p>

        <div v-if="others.length && mainVar && !fallback" class="flex flex-wrap items-center gap-2">
          <Button
            v-for="o in others"
            :key="o.id"
            size="sm"
            variant="outline"
            :disabled="connect.isPending.value"
            :title="`Adds ${mainVar}=${referenceText(alias, mainVar)} to ${o.name}`"
            @click="connect.mutate(o)"
          >
            <Plus /> Connect {{ o.name }}
          </Button>
          <span class="text-xs text-muted-foreground">Adds <span class="font-mono">{{ mainVar }}</span> to that app.</span>
        </div>
      </div>
    </CardContent>
  </Card>
</template>
