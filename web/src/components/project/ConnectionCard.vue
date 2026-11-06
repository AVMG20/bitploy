<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { Cable, ClipboardCopy, Plus } from 'lucide-vue-next'
import CopyButton from '@/components/CopyButton.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { keys, useProject, useProjectEnv, useServiceTemplates } from '@/composables/queries'
import { markRestartNeeded } from '@/composables/useRestartNeeded'
import { api } from '@/lib/api'
import { appRoute } from '@/lib/appLinks'
import { toastError, toastOk } from '@/lib/notify'
import { aliasOf, appRole, connectable, projectIdOf, projectLinks, referenceText, sharedVar, usedBy, varSource } from '@/lib/projects'
import type { App } from '@/lib/types'

// How the other apps of the project use a database (or cache, or search
// engine): who is connected, a button to connect another app, and its
// connection variables, each copied as a ${{alias.VAR}} reference that bitploy
// fills in when the other app starts.
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
const notConnected = computed(() => connectable(apps.value).filter((a) => a.id !== props.app.id && !users.value.some((u) => u.id === a.id)))
// An app with its own DATABASE_URL (or one from another database) is not offered:
// connecting would replace a working connection. It is named below instead.
const others = computed(() => notConnected.value.filter((a) => !sharedVar(env.value[a.slug], mainVar.value)))
const elsewhere = computed(() =>
  notConnected.value.flatMap((a) => {
    const v = sharedVar(env.value[a.slug], mainVar.value)
    return v ? [{ app: a, from: varSource(v) }] : []
  }),
)

const alias = computed(() => aliasOf(props.app))
// "database", "cache", "search engine": what this app is, in the sentences below.
// All the shortcuts as .env lines, for the Paste .env button of another app.
const asDotenv = computed(() => vars.value.map((v) => `${v.name}=${referenceText(alias.value, v.name)}`).join('\n') + '\n')
const what = computed(() => appRole(props.app, templates.data.value ?? []).label.toLowerCase())
const mainVar = computed(() => template.value?.main_var || template.value?.connection_vars[0] || own.data.value?.[0]?.name || '')
// The variables to show: the template's connection variables, else whatever the app has.
const vars = computed(() => {
  const names = template.value?.connection_vars.length ? template.value.connection_vars : (own.data.value ?? []).map((v) => v.name)
  return names.map((name) => ({ name, main: name === mainVar.value }))
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
      <CardTitle class="flex items-center gap-2"><Cable class="size-4 text-muted-foreground" /> Connect an app</CardTitle>
      <!-- Where it is reached (Private, at postgres:5432) is in the header above. -->
      <CardDescription>
        Other apps of {{ project?.name ?? 'this project' }} reach this {{ what }} through a variable like <span class="font-mono">{{ mainVar || 'DATABASE_URL' }}</span>.
        bitploy fills in the address and password when that app starts, so you never copy the password itself.
      </CardDescription>
    </CardHeader>
    <CardContent class="grid gap-4">
      <div class="grid gap-2 text-sm">
        <p v-if="users.length" class="flex flex-wrap items-center gap-1.5">
          <span class="text-muted-foreground">Connected:</span>
          <RouterLink v-for="u in users" :key="u.id" :to="appRoute(u, 'app-variables')" class="rounded-md border bg-muted/50 px-1.5 py-0.5 font-mono text-xs hover:border-foreground/30">{{ aliasOf(u) }}</RouterLink>
        </p>
        <p v-else class="text-muted-foreground">No app is connected yet.</p>

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
        <p v-for="e in elsewhere" :key="e.app.id" class="text-xs text-muted-foreground">
          <RouterLink :to="appRoute(e.app, 'app-variables')" class="font-mono text-foreground/85 hover:underline">{{ aliasOf(e.app) }}</RouterLink>
          already has <span class="font-mono">{{ mainVar }}</span>{{ e.from ? `, from ${e.from}` : '' }}. To switch it to {{ alias }}, set it to
          <span class="font-mono">{{ referenceText(alias, mainVar) }}</span> in its Variables.
        </p>
      </div>

      <!-- Shortcuts, not settings: a dashed box, and lines written as they go into the other app. -->
      <div class="grid gap-2 rounded-lg border border-dashed p-3">
        <div class="flex flex-wrap items-start justify-between gap-2">
          <div class="grid gap-0.5">
            <p class="flex items-center gap-1.5 text-sm font-medium"><ClipboardCopy class="size-4 text-muted-foreground" /> Shortcuts to copy</p>
            <p class="text-xs text-muted-foreground">
              Ready-made variables for your other apps. They are not settings of this {{ what }}: nothing here changes it.
              Paste one as a variable in the other app, or copy them all into its Paste .env.
            </p>
          </div>
          <CopyButton :value="asDotenv" label="Copy all as .env" variant="outline" what="Copied. Use Paste .env in the other app's Variables." />
        </div>
        <ul class="divide-y rounded-md border bg-muted/30">
          <li v-for="v in vars" :key="v.name" class="flex flex-wrap items-center gap-x-1 gap-y-1 px-3 py-1.5 font-mono text-sm">
            <span class="font-medium">{{ v.name }}</span><span class="text-muted-foreground">=</span><span class="min-w-0 truncate text-muted-foreground">{{ referenceText(alias, v.name) }}</span>
            <span v-if="v.main" class="ml-1 rounded bg-primary/10 px-1.5 py-0.5 font-sans text-[11px] text-primary">most apps need this one</span>
            <CopyButton class="ml-auto font-sans" :value="referenceText(alias, v.name)" label="Copy value" :what="`Copied. In the other app, add ${v.name} and paste this as its value.`" />
          </li>
        </ul>
      </div>
    </CardContent>
  </Card>
</template>
