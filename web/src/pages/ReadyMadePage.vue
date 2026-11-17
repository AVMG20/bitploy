<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronRight, Plus } from 'lucide-vue-next'
import { useRoute } from 'vue-router'
import CatalogPicker from '@/components/CatalogPicker.vue'
import ListSearch from '@/components/ListSearch.vue'
import { catalogMatches, categoryLabel, templateMatches } from '@/components/catalog/catalogFilter'
import PageHeader from '@/components/PageHeader.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import TemplateAddForm from '@/components/project/TemplateAddForm.vue'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { useCatalog, useProjects, useServiceTemplates } from '@/composables/queries'
import { formatMB } from '@/lib/format'
import { groupTemplates, templateRole } from '@/lib/projects'
import type { ServiceTemplate } from '@/lib/types'

// Everything bitploy can set up without your code: open-source apps with a web
// address, and the databases and caches your own apps use. ?project=<id> adds
// the pick to that project.
const route = useRoute()
const projects = useProjects()
const templates = useServiceTemplates()
const into = computed(() => (typeof route.query.project === 'string' ? route.query.project : ''))
const intoProject = computed(() => projects.data.value?.projects.find((p) => p.id === into.value) ?? null)
const services = computed(() => groupTemplates(templates.data.value ?? []).flatMap((g) => g.items))

// Search and category chips narrow both sections. The last chip is the
// databases and services; the others are the categories of the apps.
const catalog = useCatalog()
const query = ref('')
const category = ref('')
const SERVICES = '__services'
const categories = computed(() => [...new Set((catalog.data.value ?? []).map((c) => c.category))].sort())
const shownApps = computed(() => (catalog.data.value ?? []).filter((c) => catalogMatches(c, query.value, category.value === SERVICES ? '' : category.value)))
const shownServices = computed(() => services.value.filter((t) => templateMatches(t, query.value)))
const showApps = computed(() => category.value !== SERVICES)
const showServices = computed(() => !category.value || category.value === SERVICES)
const nothing = computed(() => !!query.value.trim() && !shownApps.value.length && !shownServices.value.length && !catalog.isPending.value && !templates.isPending.value)
const picked = ref<ServiceTemplate | null>(null)
const open = computed({ get: () => !!picked.value, set: (v) => !v && (picked.value = null) })
</script>

<template>
  <div class="grid gap-8">
    <PageHeader title="Ready-made" description="Open-source apps and databases that bitploy sets up for you." />

    <p v-if="intoProject" class="flex items-center gap-2 rounded-lg border border-primary/30 bg-primary/5 px-3 py-2.5 text-sm" data-testid="adding-to">
      <Plus class="size-4 shrink-0 text-primary-text" />
      <span>Adding to <span class="font-medium">{{ intoProject.name }}</span>. Pick something and it goes into this project.</span>
    </p>

    <div class="grid gap-3">
      <ListSearch v-model="query" placeholder="Search apps and databases" label="Search ready-made apps" class="sm:max-w-sm" />
      <div class="-mx-1 flex gap-1.5 overflow-x-auto px-1 pb-1 [scrollbar-width:none] sm:flex-wrap sm:overflow-visible [&::-webkit-scrollbar]:hidden" role="group" aria-label="Category">
        <button
          v-for="c in [{ id: '', label: 'All' }, ...categories.map((x) => ({ id: x, label: categoryLabel(x) })), { id: SERVICES, label: 'Databases & services' }]"
          :key="c.id"
          type="button"
          :aria-pressed="category === c.id"
          class="h-8 shrink-0 rounded-full border px-3 text-[13px] text-muted-foreground transition-colors hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none aria-pressed:border-foreground aria-pressed:bg-foreground aria-pressed:text-background"
          @click="category = c.id"
        >
{{ c.label }}
</button>
      </div>
    </div>

    <p v-if="nothing" class="rounded-lg border border-dashed px-3 py-6 text-center text-sm text-muted-foreground">Nothing matches &ldquo;{{ query.trim() }}&rdquo;.</p>

    <section v-if="showApps && !nothing" class="grid gap-3" aria-labelledby="rm-apps">
      <div>
        <h2 id="rm-apps" class="font-medium">Apps</h2>
        <p class="text-sm text-muted-foreground">Open-source apps with their own web address.</p>
      </div>
      <CatalogPicker :project="into || undefined" :query="query" :category="category" hide-intro />
    </section>

    <section v-if="showServices && !nothing" class="grid gap-3" aria-labelledby="rm-services">
      <div>
        <h2 id="rm-services" class="font-medium">Databases &amp; services</h2>
        <p class="text-sm text-muted-foreground">
          Each one is private to its project and gets its own password. Your apps get its address as a variable.
        </p>
      </div>
      <div v-if="templates.isPending.value" class="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
        <Skeleton v-for="i in 6" :key="i" class="h-20 rounded-lg" />
      </div>
      <p v-else-if="!services.length" class="rounded-lg border border-dashed px-3 py-2.5 text-sm text-muted-foreground">
        This server cannot add databases on their own. A compose file can bring its own: start a new project and paste it.
      </p>
      <p v-else-if="!shownServices.length" class="text-sm text-muted-foreground">No database or service matches.</p>
      <div v-else class="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
        <button
          v-for="t in shownServices"
          :key="t.id"
          type="button"
          class="group flex flex-col gap-2 rounded-lg border bg-card p-3 text-left transition-colors hover:bg-muted/40 focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
          @click="picked = t"
        >
          <span class="flex items-center gap-3">
            <ServiceIcon :role="templateRole(t)" :logo="t.id" />
            <span class="min-w-0 flex-1">
              <span class="block truncate text-sm font-medium">{{ t.name }}</span>
              <span class="block truncate text-xs text-muted-foreground tabular-nums">{{ categoryLabel(t.category) }} · {{ t.version }}</span>
            </span>
            <ChevronRight class="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
          </span>
          <span class="line-clamp-2 text-xs text-muted-foreground">{{ t.description }}</span>
          <span class="mt-auto text-[11px] text-muted-foreground tabular-nums">Up to {{ formatMB(t.memory_mb) }} memory<template v-if="t.volume_mb">, {{ formatMB(t.volume_mb) }} storage</template></span>
        </button>
      </div>
    </section>

    <Dialog v-model:open="open">
      <DialogContent class="max-h-[min(44rem,calc(100svh-2rem))] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Add {{ picked?.name }}{{ intoProject ? ` to ${intoProject.name}` : '' }}</DialogTitle>
          <DialogDescription>{{ intoProject ? 'It gets its own password and storage.' : 'Pick the project whose apps will use it.' }}</DialogDescription>
        </DialogHeader>
        <TemplateAddForm v-if="picked" :template="picked" :project-id="intoProject?.id" @done="picked = null" />
      </DialogContent>
    </Dialog>
  </div>
</template>
