<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { Bot, Boxes, Container, FileCode2, Github, Plus, Upload } from 'lucide-vue-next'
import PageHeader from '@/components/PageHeader.vue'
import QueryError from '@/components/QueryError.vue'
import StorageAppsDialog from '@/components/StorageAppsDialog.vue'
import ProjectCard from '@/components/project/ProjectCard.vue'
import UsageCard from '@/components/UsageCard.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useMe, useProjects, useServiceTemplates } from '@/composables/queries'
import { CPU_HINT, cpuFromPercent, formatCPU, formatCPUMillis, formatMB } from '@/lib/format'
import { usageBar } from '@/lib/usage'

// Home: every project as a card, with the apps inside it, in the order of
// the sidebar (dragged there; a new project on top).
const me = useMe()
const projects = useProjects()
const templates = useServiceTemplates()

const usage = computed(() => me.data.value?.usage)
const quota = computed(() => me.data.value?.quota)
// Storage allocated counts each saved-files volume at its full size (as the server checks it); in use counts what is in it.
const storageTaken = computed(() => (usage.value ? usage.value.files_mb + usage.value.backups_mb + usage.value.volume_reserved_mb : 0))
const storageUsed = computed(() => (usage.value ? usage.value.files_mb + usage.value.backups_mb + usage.value.volume_used_mb : 0))
// An admin can let someone use more memory and CPU than the limits: nothing is checked, so those cards say "No limit".
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
  usage.value && quota.value && quota.value.volume_mb > 0 && storageTaken.value > quota.value.volume_mb ? overText('storage', 'Delete old backups or saved files') : '',
)
const appsAlert = computed(() =>
  usage.value && quota.value && usage.value.apps > quota.value.apps ? overText('apps', 'Delete the apps you no longer need before adding one') : '',
)
// A first visit with no projects: the cards would only show zeros.
const showStats = computed(() => projects.isPending.value || all.value.length > 0)

const all = computed(() => projects.data.value?.projects ?? [])
// Clicking the Storage card shows what each of your apps takes.
const storageOpen = ref(false)

// Below sm the four cards would push the projects off the screen: one compact figure and bar each.
const strip = computed(() => {
  const u = usage.value
  const q = quota.value
  if (!u || !q) return []
  const row = (label: string, used: number, allocated: number, max: number | null, text: string, alert: string) => ({
    label,
    text,
    alert,
    bar: usageBar(used, allocated, max),
  })
  return [
    row('memory', u.memory_used_mb, u.memory_reserved_mb, unlimited.value ? null : q.memory_mb, `${formatMB(u.memory_used_mb)}${unlimited.value ? '' : ` / ${formatMB(q.memory_mb)}`}`, memAlert.value),
    row('CPU', cpuFromPercent(u.cpu_now_percent) * 1000, u.cpu_reserved_millis, unlimited.value ? null : q.cpu_millis, `${formatCPU(cpuFromPercent(u.cpu_now_percent)).replace(' CPU', '')} / ${unlimited.value ? 'no limit' : formatCPUMillis(q.cpu_millis)}`, cpuAlert.value),
    row('storage', storageUsed.value, storageTaken.value, q.volume_mb, `${formatMB(storageUsed.value)} / ${formatMB(q.volume_mb)}`, storageAlert.value),
    row('apps running', u.apps_running, u.apps, q.apps, `${u.apps_running} / ${q.apps}`, appsAlert.value),
  ]
})
const stripTone = (level: string) => (level === 'ok' ? 'bg-primary' : level === 'warn' ? 'bg-warning' : 'bg-destructive')

// Connecting an AI assistant first, then the same choices, names and words
// as the first step of New project.
const SOURCES = [
  { key: 'ai', icon: Bot, title: 'Connect your AI assistant', text: 'Claude, Cursor or VS Code puts your code online for you. You say "deploy this folder".' },
  { key: 'github', icon: Github, title: 'GitHub', text: 'Pick a repository. bitploy works out how to build it.' },
  { key: 'upload', icon: Upload, title: 'Upload', text: 'A folder, a .zip or one .html page from your computer.' },
  { key: 'dockerfile', icon: FileCode2, title: 'Dockerfile or compose', text: 'Paste a Dockerfile, or a compose file for several services.' },
  { key: 'image', icon: Container, title: 'Docker image', text: 'A published image, like nginx:1.27 or ghcr.io/owner/app.' },
  { key: 'ready-made', icon: Boxes, title: 'Ready-made', text: 'Well-known apps set up for you, like Uptime Kuma, Vaultwarden or PocketBase.' },
] as const
function sourceTo(key: string) {
  if (key === 'ai') return { name: 'connect' }
  return key === 'ready-made' ? { name: 'ready-made' } : { name: 'new-app', query: { source: key } }
}
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Projects" description="Each project holds the apps of one product: a website, its API, its database.">
      <Button v-if="all.length" as-child><RouterLink :to="{ name: 'new-app' }"><Plus /> New project</RouterLink></Button>
    </PageHeader>

    <!-- Each card: in use right now (solid), allocated by the apps (light), and the most that may be allocated (the whole bar). -->
    <div v-if="showStats && strip.length" class="grid grid-cols-2 gap-x-5 gap-y-3 rounded-xl border bg-card px-4 py-3 sm:hidden" data-testid="usage-strip">
      <component
        :is="r.label === 'storage' ? 'button' : 'div'"
        v-for="r in strip"
        :key="r.label"
        :type="r.label === 'storage' ? 'button' : undefined"
        class="grid min-w-0 gap-1 text-left"
        @click="r.label === 'storage' && (storageOpen = true)"
      >
        <p class="truncate text-xs text-muted-foreground"><span class="font-medium text-foreground tabular-nums">{{ r.text }}</span> {{ r.label }}</p>
        <div class="relative h-1 w-full overflow-hidden rounded-full bg-muted" role="img" :aria-label="`${r.label}: ${r.text}${r.alert ? '. ' + r.alert : ''}`">
          <div :class="['absolute inset-y-0 left-0 rounded-full opacity-30', stripTone(r.bar.level)]" :style="{ width: `${r.bar.allocatedPct}%` }" />
          <div :class="['absolute inset-y-0 left-0 rounded-full', stripTone(r.bar.level)]" :style="{ width: `${r.bar.usedPct}%` }" />
        </div>
      </component>
      <p v-for="r in strip.filter((x) => x.alert)" :key="r.label" class="col-span-2 text-xs font-medium text-destructive" role="status">{{ r.alert }}</p>
    </div>
    <Skeleton v-else-if="showStats && me.isPending.value" class="h-24 rounded-xl sm:hidden" />
    <div v-if="showStats" class="hidden grid-cols-2 gap-4 sm:grid lg:grid-cols-4">
      <UsageCard
        title="Memory"
        :loading="me.isPending.value"
        :value="usage ? formatMB(usage.memory_used_mb) : ''"
        :used="usage?.memory_used_mb ?? 0"
        :allocated="usage?.memory_reserved_mb ?? 0"
        :max="unlimited ? null : (quota?.memory_mb ?? null)"
        :allocated-text="usage ? formatMB(usage.memory_reserved_mb) : ''"
        :max-text="quota ? formatMB(quota.memory_mb) : ''"
        :alert="memAlert"
        hint="In use: what your apps use right now. Allocated: the memory limits of all your apps added up, databases included."
      />
      <UsageCard
        title="CPU"
        :loading="me.isPending.value"
        :value="usage ? formatCPU(cpuFromPercent(usage.cpu_now_percent)).replace(' CPU', '') : ''"
        value-label="CPU in use"
        :used="usage ? cpuFromPercent(usage.cpu_now_percent) * 1000 : 0"
        :allocated="usage?.cpu_reserved_millis ?? 0"
        :max="unlimited ? null : (quota?.cpu_millis ?? null)"
        :allocated-text="usage ? formatCPUMillis(usage.cpu_reserved_millis) : ''"
        :max-text="quota ? formatCPUMillis(quota.cpu_millis) : ''"
        :alert="cpuAlert"
        :hint="`In use: what your apps use right now. Allocated: the CPU limits of all your apps added up. ${CPU_HINT}`"
      />
      <UsageCard
        title="Storage"
        :loading="me.isPending.value"
        :value="usage ? formatMB(storageUsed) : ''"
        :used="storageUsed"
        :allocated="storageTaken"
        :max="quota?.volume_mb ?? null"
        :allocated-text="usage ? formatMB(storageTaken) : ''"
        :max-text="quota ? formatMB(quota.volume_mb) : ''"
        :alert="storageAlert"
        hint="App files, backups and saved files (/data). Allocated counts each saved-files volume at its full size."
        open-label="See storage per app"
        @open="storageOpen = true"
      />
      <UsageCard
        title="Apps"
        :loading="me.isPending.value"
        :value="usage ? String(usage.apps_running) : ''"
        value-label="running"
        :used="usage?.apps_running ?? 0"
        :allocated="usage?.apps ?? 0"
        :max="quota?.apps ?? null"
        :allocated-text="usage ? String(usage.apps) : ''"
        :allocated-label="usage?.apps === 1 ? 'app' : 'apps'"
        :max-text="quota ? String(quota.apps) : ''"
        :alert="appsAlert"
        hint="Databases and caches count as apps."
      />
    </div>

    <StorageAppsDialog
      v-if="me.data.value"
      v-model:open="storageOpen"
      :projects="all"
      :owner-id="me.data.value.user.id"
      :used-mb="storageUsed"
      :allocated-mb="storageTaken"
      :max-mb="quota?.volume_mb || null"
    />

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
      <div class="grid w-full max-w-4xl gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <RouterLink
          v-for="s in SOURCES"
          :key="s.key"
          :to="sourceTo(s.key)"
          class="group grid justify-items-start gap-2 rounded-xl border bg-card p-4 text-left shadow-card transition-colors hover:border-foreground/25"
        >
          <span :class="['flex size-9 items-center justify-center rounded-lg border', s.key === 'ai' ? 'border-primary/30 bg-primary/10 text-primary-text' : 'bg-muted/60 text-muted-foreground group-hover:text-foreground']">
            <component :is="s.icon" class="size-4" />
          </span>
          <span class="text-sm font-medium">{{ s.title }}</span>
          <span class="text-xs text-muted-foreground">{{ s.text }}</span>
        </RouterLink>
      </div>
    </section>

    <section v-else class="grid gap-3" aria-label="Projects">
      <p class="text-xs text-muted-foreground">{{ all.length }} {{ all.length === 1 ? 'project' : 'projects' }}</p>

      <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <ProjectCard v-for="p in all" :key="p.id" :project="p" :templates="templates.data.value ?? []" />
        <RouterLink
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
