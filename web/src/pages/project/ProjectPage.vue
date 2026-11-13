<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { ArrowUpRight, Play, Plus, Settings2, Square, Users } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyForAiButton from '@/components/CopyForAiButton.vue'
import EmptyState from '@/components/EmptyState.vue'
import PageHeader from '@/components/PageHeader.vue'
import QueryError from '@/components/QueryError.vue'
import StatusDot from '@/components/StatusDot.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { keys, useActiveBuilds } from '@/composables/queries'
import { openAdd } from '@/composables/useAddToProject'
import { useProjectData } from '@/composables/useProjectData'
import { api } from '@/lib/api'
import { appRoute } from '@/lib/appLinks'
import { appStatusInfo, formatMB, hostOf } from '@/lib/format'
import { toastError, toastOk, toastWarn } from '@/lib/notify'
import { aliasOf, isPublicApp, mainApp, ownerName, projectHealth, projectHealthText, projectUsage } from '@/lib/projects'
import type { ProjectPowerResult } from '@/lib/types'

// A project: its name and health on top, then the canvas of its apps, the
// variables and connections of all of them, and its settings.
const props = defineProps<{ projectId: string }>()
const route = useRoute()
const { list, project, fallback, apps, links, roleOf } = useProjectData(() => props.projectId)
const health = computed(() => projectHealth(apps.value))
const { phaseOf } = useActiveBuilds()
const usage = computed(() => projectUsage(apps.value))
const main = computed(() => (project.value ? mainApp(project.value) : undefined))

// route.fullPath too: the router sets its own title after every navigation, also when only ?app= changes.
watch(
  () => [project.value?.name, route.meta.title, route.fullPath] as const,
  ([n, tab]) => {
    if (n) document.title = `${tab ? `${tab} - ` : ''}${n} - bitploy`
  },
  { immediate: true, flush: 'post' },
)

const TABS = [
  { name: 'project', label: 'Canvas' },
  { name: 'project-variables', label: 'Variables' },
  { name: 'project-members', label: 'Members' },
  { name: 'project-settings', label: 'Settings' },
]

// Start all / stop all. Stop all asks first (visitors see the stopped page);
// start all starts the databases first, the server keeps that order.
// An admin looking at someone else's project gets neither.
const qc = useQueryClient()
const canPower = computed(() => !!project.value && project.value.role !== 'admin' && !fallback.value)
const runningCount = computed(() => apps.value.filter((a) => a.desired_state === 'running').length)
const startableCount = computed(() => apps.value.filter((a) => a.desired_state !== 'running' && a.current_deployment).length)
const confirmStop = ref(false)
const plural = (n: number) => (n === 1 ? '1 app' : `${n} apps`)
const power = useMutation({
  mutationFn: (start: boolean) => (start ? api.startProject(props.projectId) : api.stopProject(props.projectId)),
  onSuccess: (r: ProjectPowerResult, start) => {
    const verb = start ? 'Started' : 'Stopped'
    if (r.failed.length) {
      const what = r.failed.map((f) => `${f.app}: ${f.message}`).join(' ')
      toastWarn(`${verb} ${plural(r.changed.length)}; ${plural(r.failed.length)} did not ${start ? 'start' : 'stop'}`, what)
    } else if (r.changed.length) {
      toastOk(`${verb} ${plural(r.changed.length)}`)
    } else {
      toastOk(start ? 'Nothing to start' : 'Nothing to stop')
    }
  },
  onError: (e) => toastError(e),
  onSettled: () => {
    confirmStop.value = false
    void qc.invalidateQueries({ queryKey: keys.apps })
  },
})

const projectForAi = computed(() => {
  const p = project.value
  if (!p) return ''
  const lines = [`bitploy project "${p.name}" (id ${p.id}) with ${p.apps.length} app${p.apps.length === 1 ? '' : 's'}:`]
  for (const a of apps.value) {
    const r = roleOf(a)
    const where = isPublicApp(a) ? a.url : `private, at ${a.internal_host || `${aliasOf(a)}:${a.port ?? ''}`}`
    lines.push(`- ${aliasOf(a)} (slug ${a.slug}): ${r.product ? `${r.label}, ${r.product}` : r.label}, ${appStatusInfo(a.status).label.toLowerCase()}, ${where}`)
  }
  for (const l of links.value) {
    const from = apps.value.find((a) => a.id === l.from)
    const to = apps.value.find((a) => a.id === l.to)
    if (from && to) lines.push(`- ${aliasOf(from)} reads ${l.vars.join(', ')} from ${aliasOf(to)}`)
  }
  lines.push('Apps of a project reach each other by their short name; a variable can point at another app as ${{alias.VAR}}.')
  return lines.join('\n')
})
</script>

<template>
  <div class="grid grid-cols-[minmax(0,1fr)] gap-6">
    <QueryError v-if="list.isError.value && !list.data.value" :error="list.error.value" @retry="list.refetch()" />
    <div v-else-if="list.isPending.value" class="grid gap-4">
      <Skeleton class="h-8 w-64" />
      <Skeleton class="h-96 w-full rounded-xl" />
    </div>
    <EmptyState v-else-if="!project" title="Project not found." description="It may have been deleted, or the address has a typo.">
      <Button as-child><RouterLink to="/">Back to your projects</RouterLink></Button>
    </EmptyState>

    <template v-else>
      <PageHeader :title="project.name">
        <template v-if="health.total || project.shared" #badges>
          <span v-if="health.total" class="inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs text-muted-foreground">
            <StatusDot :status="health.status === 'empty' ? 'new' : health.status" :building="phaseOf(apps)" aria-hidden="true" />
            {{ projectHealthText(apps) }}
          </span>
          <span v-if="project.shared" class="inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs text-muted-foreground" :title="`${ownerName(project)} shared this project with you`">
            <Users class="size-3" aria-hidden="true" />
            Shared by {{ ownerName(project) }}
          </span>
        </template>
        <template v-if="apps.length" #meta>
          <a v-if="main && isPublicApp(main)" :href="main.url" target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-1 font-mono text-[13px] text-foreground hover:underline">
            {{ hostOf(main.url) }} <ArrowUpRight class="size-3 text-muted-foreground" />
          </a>
          <span v-else>No web address yet</span>
          <span>{{ apps.length === 1 ? '1 app' : `${apps.length} apps` }}<template v-if="usage.memoryMB">, up to {{ formatMB(usage.memoryMB) }} memory</template></span>
        </template>
        <template v-if="canPower">
          <Button v-if="startableCount" variant="outline" :disabled="power.isPending.value" data-testid="project-start-all" @click="power.mutate(true)">
            <Play /> {{ power.isPending.value && power.variables.value ? 'Starting...' : 'Start all' }}
          </Button>
          <Button v-if="runningCount" variant="outline" :disabled="power.isPending.value" data-testid="project-stop-all" @click="confirmStop = true">
            <Square /> Stop all
          </Button>
        </template>
        <CopyForAiButton v-if="apps.length" :text="projectForAi" />
        <Button v-if="fallback && main" variant="outline" as-child><RouterLink :to="appRoute(main, 'app-settings')"><Settings2 /> Settings</RouterLink></Button>
        <Button :disabled="fallback" :title="fallback ? 'This server keeps one app per project.' : undefined" @click="openAdd(project.id)"><Plus /> Add</Button>
      </PageHeader>

      <Tabs v-if="!fallback" :model-value="String(route.name)" class="min-w-0">
        <div class="overflow-x-auto overflow-y-hidden border-b">
          <TabsList variant="line" class="-ml-1.5">
            <TabsTrigger v-for="t in TABS" :key="t.name" :value="t.name" as-child>
              <RouterLink :to="{ name: t.name, params: { projectId: project.id } }">{{ t.label }}</RouterLink>
            </TabsTrigger>
          </TabsList>
        </div>
      </Tabs>

      <RouterView :project-id="project.id" />

      <ConfirmDialog
        v-model:open="confirmStop"
        :title="`Stop all apps of ${project.name}?`"
        :description="`${plural(runningCount)} stop, the databases and services last. Visitors see a page that says the app is stopped until you start it again. Their data stays.`"
        confirm-label="Stop all"
        :loading="power.isPending.value"
        @confirm="power.mutate(false)"
      />
    </template>
  </div>
</template>
