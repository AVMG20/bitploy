<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Boxes, ChevronRight, Container, FileCode2, Github, Upload } from 'lucide-vue-next'
import ServiceIcon from '@/components/ServiceIcon.vue'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { useMe, useProjects, useServiceTemplates } from '@/composables/queries'
import { addTarget } from '@/composables/useAddToProject'
import { formatMB } from '@/lib/format'
import { groupTemplates, templateRole } from '@/lib/projects'
import type { ServiceTemplate } from '@/lib/types'
import TemplateAddForm from './TemplateAddForm.vue'

// "+ Add" on a project: your own code (GitHub, upload, Docker), or a database,
// cache or search engine that bitploy sets up with its own password and
// connects to the apps you pick. Mounted once in the layout; openAdd() opens it.
const router = useRouter()
const projects = useProjects()
const templates = useServiceTemplates()
const me = useMe()

const open = computed({
  get: () => !!addTarget.value,
  set: (v) => {
    if (!v) addTarget.value = null
  },
})
const project = computed(() => projects.data.value?.projects.find((p) => p.id === addTarget.value?.projectId) ?? null)
const fallback = computed(() => !!projects.data.value?.fallback)
const picked = ref<ServiceTemplate | null>(null)
const fromStart = ref(false)
watch(
  [addTarget, () => templates.data.value],
  ([t, list]) => {
    if (!t) {
      picked.value = null
      return
    }
    if (t.template && list) {
      picked.value = list.find((x) => x.id === t.template) ?? null
      fromStart.value = !!picked.value
    } else if (!t.template) fromStart.value = false
  },
  { immediate: true },
)

const groups = computed(() => groupTemplates(templates.data.value ?? []))
const memLeft = computed(() => (me.data.value && !me.data.value.quota.overallocate ? Math.max(0, me.data.value.quota.memory_mb - me.data.value.usage.memory_reserved_mb) : null))

const SOURCES = [
  { key: 'github', icon: Github, title: 'GitHub repository', text: 'Any repository. bitploy finds its Dockerfile, compose file, Node.js app or website.' },
  { key: 'upload', icon: Upload, title: 'Upload', text: 'A folder, a .zip or one .html page from your computer.' },
  { key: 'dockerfile', icon: FileCode2, title: 'Dockerfile or compose', text: 'Paste a Dockerfile, or a compose file for several services.' },
  { key: 'image', icon: Container, title: 'Docker image', text: 'A published image, like nginx:1.27 or ghcr.io/owner/app.' },
] as const

function go(source: string) {
  const id = addTarget.value?.projectId
  addTarget.value = null
  // Without projects on the server, new code starts its own project.
  void router.push({ name: 'new-app', query: fallback.value || !id ? { source } : { source, project: id } })
}
function readyMade() {
  const id = addTarget.value?.projectId
  addTarget.value = null
  void router.push({ name: 'ready-made', query: fallback.value || !id ? {} : { project: id } })
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="max-h-[min(44rem,calc(100svh-2rem))] gap-0 overflow-y-auto p-0 sm:max-w-2xl">
      <template v-if="picked && addTarget">
        <DialogHeader class="border-b px-5 py-4">
          <DialogTitle>Add {{ picked.name }} to {{ project?.name ?? 'the project' }}</DialogTitle>
          <DialogDescription>It gets its own password and storage. The apps you connect get its address as a variable.</DialogDescription>
        </DialogHeader>
        <div class="px-5 py-4">
          <TemplateAddForm :template="picked" :project-id="addTarget.projectId" :show-back="!fromStart" @back="picked = null" @done="open = false" />
        </div>
      </template>

      <template v-else>
        <DialogHeader class="border-b px-5 py-4">
          <DialogTitle>Add to {{ project?.name ?? 'the project' }}</DialogTitle>
          <DialogDescription>Apps of a project reach each other by their short name, like postgres:5432.</DialogDescription>
        </DialogHeader>

        <section class="grid gap-2 px-5 pt-4" aria-labelledby="add-code">
          <h3 id="add-code" class="text-xs font-medium text-muted-foreground">Your code</h3>
          <div class="grid gap-2 sm:grid-cols-2">
            <button
              v-for="s in SOURCES"
              :key="s.key"
              type="button"
              class="group flex items-start gap-3 rounded-lg border bg-card p-3 text-left transition-colors hover:border-foreground/25 hover:bg-accent/40"
              @click="go(s.key)"
            >
              <span class="flex size-8 shrink-0 items-center justify-center rounded-md border bg-muted/60 text-muted-foreground group-hover:text-foreground"><component :is="s.icon" class="size-4" /></span>
              <span class="min-w-0">
                <span class="block text-sm font-medium">{{ s.title }}</span>
                <span class="block text-xs text-muted-foreground">{{ s.text }}</span>
              </span>
            </button>
          </div>
          <p v-if="fallback" class="text-xs text-muted-foreground">This server keeps one app per project, so new code starts a project of its own.</p>
        </section>

        <section v-for="g in groups" :key="g.key" class="grid gap-2 px-5 pt-5" :aria-labelledby="`add-${g.key}`">
          <h3 :id="`add-${g.key}`" class="text-xs font-medium text-muted-foreground">{{ g.label }}</h3>
          <div class="grid gap-2 sm:grid-cols-2">
            <button
              v-for="t in g.items"
              :key="t.id"
              type="button"
              class="group flex items-start gap-3 rounded-lg border bg-card p-3 text-left transition-colors hover:border-foreground/25 hover:bg-accent/40"
              @click="picked = t"
            >
              <ServiceIcon :role="templateRole(t)" size="sm" class="mt-0.5" />
              <span class="min-w-0 flex-1">
                <span class="flex items-baseline gap-1.5 text-sm font-medium">{{ t.name }} <span class="text-xs font-normal text-muted-foreground">{{ t.version }}</span></span>
                <span class="block text-xs text-muted-foreground">{{ t.description }}</span>
                <span class="mt-1.5 block text-[11px] text-muted-foreground tabular-nums">
                  Up to {{ formatMB(t.memory_mb) }} memory<template v-if="t.volume_mb">, {{ formatMB(t.volume_mb) }} storage</template>
                  <span v-if="memLeft !== null && memLeft < t.memory_mb" class="block font-medium text-destructive">You have {{ formatMB(memLeft) }} memory left</span>
                </span>
              </span>
              <ChevronRight class="mt-1 size-4 shrink-0 text-muted-foreground/60 group-hover:text-foreground" />
            </button>
          </div>
        </section>

        <div v-if="templates.isPending.value" class="grid gap-2 px-5 pt-5 sm:grid-cols-2">
          <Skeleton v-for="i in 4" :key="i" class="h-20 rounded-lg" />
        </div>
        <p v-else-if="!groups.length" class="mx-5 mt-5 rounded-lg border border-dashed px-3 py-2.5 text-xs text-muted-foreground">
          This server cannot add databases on their own. A compose file can bring its own: choose "Dockerfile or compose" above.
        </p>

        <div class="px-5 pt-5 pb-5">
          <button
            type="button"
            class="group flex w-full items-center gap-3 rounded-lg border border-dashed p-3 text-left transition-colors hover:border-foreground/25 hover:bg-accent/40"
            @click="readyMade"
          >
            <span class="flex size-8 shrink-0 items-center justify-center rounded-md border bg-muted/60 text-muted-foreground"><Boxes class="size-4" /></span>
            <span class="min-w-0 flex-1">
              <span class="block text-sm font-medium">Ready-made app</span>
              <span class="block text-xs text-muted-foreground">Open-source apps set up for you: Uptime Kuma, Vaultwarden, PocketBase and more.</span>
            </span>
            <ChevronRight class="size-4 shrink-0 text-muted-foreground/60 group-hover:text-foreground" />
          </button>
        </div>
      </template>
    </DialogContent>
  </Dialog>
</template>
