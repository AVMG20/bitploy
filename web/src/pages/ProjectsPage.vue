<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { Boxes, Container, FileCode2, Github, Plus, SearchX, Upload } from 'lucide-vue-next'
import EmptyState from '@/components/EmptyState.vue'
import ListSearch from '@/components/ListSearch.vue'
import PageHeader from '@/components/PageHeader.vue'
import QueryError from '@/components/QueryError.vue'
import StatCard from '@/components/StatCard.vue'
import ProjectCard from '@/components/project/ProjectCard.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useMe, useProjects, useServiceTemplates } from '@/composables/queries'
import { useQueryParam } from '@/composables/useQueryParam'
import { CPU_HINT, cpuFromPercent, formatCPU, formatCPUMillis, formatMB, pct } from '@/lib/format'
import { aliasOf, projectHealth } from '@/lib/projects'
import { projectRoute } from '@/lib/appLinks'

// Home: every project as a card, with the apps inside it. A project with a
// problem comes first, then the most recently deployed.
const router = useRouter()
const me = useMe()
const projects = useProjects()
const templates = useServiceTemplates()

const usage = computed(() => me.data.value?.usage)
const quota = computed(() => me.data.value?.quota)
const storageTaken = computed(() => (usage.value ? usage.value.files_mb + usage.value.backups_mb + usage.value.volume_reserved_mb : 0))
// An admin can let someone use more memory and CPU than the limits: nothing is checked, so there is no "of" and no bar.
const unlimited = computed(() => !!quota.value?.overallocate)
const isAdmin = computed(() => me.data.value?.user.role === 'admin')
/** What to do when over a limit: an admin raises their own, everyone else asks. */
function overText(what: string, fix: string): string {
  return `Over your ${what} limit. ${fix}, or ${isAdmin.value ? 'raise your limit in Admin, People' : 'ask your admin for more'}.`
}
const memAlert = computed(() =>
  usage.value && quota.value && !unlimited.value && usage.value.memory_reserved_mb > quota.value.memory_mb
    ? overText('memory', 'New deploys that need more memory fail. Lower the memory of an app or stop one')
    : '',
)
const cpuAlert = computed(() =>
  usage.value && quota.value && !unlimited.value && usage.value.cpu_reserved_millis > quota.value.cpu_millis
    ? overText('CPU', 'New deploys that need more CPU fail. Lower the CPU of an app or stop one')
    : '',
)
const storageAlert = computed(() =>
  usage.value && quota.value && storageTaken.value > quota.value.volume_mb ? overText('storage', 'Delete old backups or saved files') : '',
)
const appsAlert = computed(() =>
  usage.value && quota.value && usage.value.apps > quota.value.apps ? overText('apps', 'Delete the apps you no longer need before adding one') : '',
)
// A first visit with no projects: the cards would only show zeros.
const showStats = computed(() => projects.isPending.value || all.value.length > 0)

const search = useQueryParam('q', '')
const all = computed(() => projects.data.value?.projects ?? [])
const URGENT: Record<string, number> = { failed: 0, crashed: 0, deploying: 1 }
function lastDeploy(p: (typeof all.value)[number]): string {
  let at = p.created_at
  for (const a of p.apps) {
    const d = a.current_deployment?.finished_at ?? a.current_deployment?.queued_at
    if (d && d > at) at = d
  }
  return at
}
const shown = computed(() => {
  const q = search.value.trim().toLowerCase()
  const match = (p: (typeof all.value)[number]) =>
    !q || p.name.toLowerCase().includes(q) || p.apps.some((a) => a.name.toLowerCase().includes(q) || aliasOf(a).includes(q) || a.url.toLowerCase().includes(q))
  return all.value
    .filter(match)
    .map((p) => ({ p, urgent: URGENT[projectHealth(p.apps).status] ?? 9, at: lastDeploy(p) }))
    .sort((a, b) => a.urgent - b.urgent || b.at.localeCompare(a.at))
    .map((x) => x.p)
})
const searchEl = ref<InstanceType<typeof ListSearch> | null>(null)
function openOnly() {
  if (shown.value.length === 1) void router.push(projectRoute(shown.value[0]!.id))
}

// The same choices, names and words as the first step of New project.
const SOURCES = [
  { key: 'github', icon: Github, title: 'GitHub', text: 'Pick a repository. bitploy works out how to build it.' },
  { key: 'upload', icon: Upload, title: 'Upload', text: 'A folder, a .zip or one .html page from your computer.' },
  { key: 'dockerfile', icon: FileCode2, title: 'Dockerfile or compose', text: 'Paste a Dockerfile, or a compose file for several services.' },
  { key: 'image', icon: Container, title: 'Docker image', text: 'A published image, like nginx:1.27 or ghcr.io/owner/app.' },
  { key: 'ready-made', icon: Boxes, title: 'Ready-made', text: 'Well-known apps set up for you, like Uptime Kuma, Vaultwarden or PocketBase.' },
] as const
function sourceTo(key: string) {
  return key === 'ready-made' ? { name: 'ready-made' } : { name: 'new-app', query: { source: key } }
}
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Projects" description="Each project holds the apps of one product: a website, its API, its database.">
      <Button v-if="all.length" as-child><RouterLink :to="{ name: 'new-app' }"><Plus /> New project</RouterLink></Button>
    </PageHeader>

    <!-- Memory and CPU count the limits of all your apps (what each may use at most), not what they use right now. -->
    <div v-if="showStats" class="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-4">
      <StatCard
        title="Memory"
        :loading="me.isPending.value"
        :value="usage ? formatMB(usage.memory_reserved_mb) : ''"
        :suffix="quota && !unlimited ? `of ${formatMB(quota.memory_mb)} allowed` : ''"
        :progress="usage && quota && !unlimited ? pct(usage.memory_reserved_mb, quota.memory_mb) : undefined"
        :sub="usage ? `${unlimited ? 'No limit on memory and CPU for you. ' : ''}Your apps use ${formatMB(usage.memory_used_mb)} right now.` : ''"
        :alert="memAlert"
        hint="The memory limits of all your apps added up, databases included."
      />
      <StatCard
        title="CPU"
        :loading="me.isPending.value"
        :value="usage ? formatCPUMillis(usage.cpu_reserved_millis).replace(' CPU', '') : ''"
        :suffix="quota && !unlimited ? `of ${formatCPUMillis(quota.cpu_millis)} allowed` : 'CPU'"
        :progress="usage && quota && !unlimited ? pct(usage.cpu_reserved_millis, quota.cpu_millis) : undefined"
        :sub="usage ? `${unlimited ? 'No limit on memory and CPU for you. ' : ''}Your apps use ${formatCPU(cpuFromPercent(usage.cpu_now_percent))} right now.` : ''"
        :alert="cpuAlert"
        :hint="`The CPU limits of all your apps added up. ${CPU_HINT}`"
      />
      <StatCard
        title="Storage"
        :loading="me.isPending.value"
        :value="usage ? formatMB(storageTaken) : ''"
        :suffix="quota ? `of ${formatMB(quota.volume_mb)} allowed` : ''"
        :progress="usage && quota ? pct(storageTaken, quota.volume_mb) : undefined"
        :sub="usage ? (usage.volume_reserved_mb ? `Includes ${formatMB(usage.volume_reserved_mb)} kept free for saved files.` : 'App files and backups.') : ''"
        :alert="storageAlert"
      />
      <StatCard
        title="Apps"
        :loading="me.isPending.value"
        :value="usage ? String(usage.apps) : ''"
        :suffix="quota ? `of ${quota.apps} allowed` : ''"
        :progress="usage && quota ? pct(usage.apps, quota.apps) : undefined"
        :sub="usage ? `${usage.apps_running} running, ${usage.deploys_week} deploys this week` : ''"
        :alert="appsAlert"
        hint="Databases and caches count as apps."
      />
    </div>

    <QueryError v-if="projects.isError.value && !projects.data.value" :error="projects.error.value" @retry="projects.refetch()" />

    <div v-else-if="projects.isPending.value" class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
      <Skeleton v-for="i in 3" :key="i" class="h-56 rounded-xl" />
    </div>

    <!-- No projects: where the code comes from, as the first step. -->
    <section v-else-if="!all.length" class="canvas-dots grid justify-items-center gap-6 rounded-xl border px-4 py-14 text-center" aria-labelledby="first-project">
      <div class="grid max-w-lg gap-2">
        <h2 id="first-project" class="text-xl font-semibold tracking-tight">Deploy your first project</h2>
        <p class="text-sm text-muted-foreground">Where is your code?</p>
      </div>
      <div class="grid w-full max-w-5xl gap-3 sm:grid-cols-2 lg:grid-cols-5">
        <RouterLink
          v-for="s in SOURCES"
          :key="s.key"
          :to="sourceTo(s.key)"
          class="group grid justify-items-start gap-2 rounded-xl border bg-card p-4 text-left shadow-card transition-colors hover:border-foreground/25"
        >
          <span :class="['flex size-9 items-center justify-center rounded-lg border', s.key === 'github' ? 'border-primary/30 bg-primary/10 text-primary' : 'bg-muted/60 text-muted-foreground group-hover:text-foreground']">
            <component :is="s.icon" class="size-4" />
          </span>
          <span class="text-sm font-medium">{{ s.title }}</span>
          <span class="text-xs text-muted-foreground">{{ s.text }}</span>
        </RouterLink>
      </div>
      <p class="max-w-lg text-xs text-muted-foreground">
        Working with an AI assistant? <RouterLink to="/connect" class="text-foreground underline-offset-4 hover:underline">Connect it</RouterLink> and say "deploy this folder to bitploy".
      </p>
    </section>

    <section v-else class="grid gap-3" aria-label="Projects">
      <div class="flex items-center gap-3">
        <ListSearch ref="searchEl" v-model="search" class="flex-1 sm:max-w-sm" placeholder="Search projects and apps" label="Search projects" @enter="openOnly" />
        <p class="ml-auto hidden shrink-0 text-xs text-muted-foreground sm:block" aria-live="polite">
          <template v-if="search.trim()">{{ shown.length }} of {{ all.length }} projects</template>
          <template v-else>{{ all.length }} {{ all.length === 1 ? 'project' : 'projects' }}</template>
        </p>
      </div>

      <EmptyState v-if="!shown.length" :icon="SearchX" :title="`No projects match “${search.trim()}”.`" description="Search looks at project names, app names and addresses.">
        <Button variant="outline" @click="search = ''; searchEl?.focus()">Clear search</Button>
      </EmptyState>

      <div v-else class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <ProjectCard v-for="p in shown" :key="p.id" :project="p" :templates="templates.data.value ?? []" />
        <RouterLink
          v-if="!search.trim()"
          :to="{ name: 'new-app' }"
          class="grid min-h-48 place-items-center rounded-xl border border-dashed text-sm text-muted-foreground transition-colors hover:border-foreground/30 hover:text-foreground"
        >
          <span class="grid justify-items-center gap-2">
            <span class="flex size-9 items-center justify-center rounded-lg border bg-card"><Plus class="size-4" /></span>
            New project
          </span>
        </RouterLink>
      </div>
      <p v-if="projects.data.value?.fallback" class="text-xs text-muted-foreground">
        <Container class="mr-1 inline size-3.5 align-[-2px]" />This server shows each app as its own project. It cannot put several apps in one project.
      </p>
    </section>
  </div>
</template>
