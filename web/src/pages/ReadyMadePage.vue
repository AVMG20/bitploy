<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronRight } from 'lucide-vue-next'
import { useRoute } from 'vue-router'
import CatalogPicker from '@/components/CatalogPicker.vue'
import PageHeader from '@/components/PageHeader.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import TemplateAddForm from '@/components/project/TemplateAddForm.vue'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { useProjects, useServiceTemplates } from '@/composables/queries'
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
const categoryLabel = (c: string) => (c === 'databases' ? 'Database' : c.charAt(0).toUpperCase() + c.slice(1))
const picked = ref<ServiceTemplate | null>(null)
const open = computed({ get: () => !!picked.value, set: (v) => !v && (picked.value = null) })
</script>

<template>
  <div class="grid gap-8">
    <PageHeader
      title="Ready-made"
      :description="intoProject ? `Pick something to add to ${intoProject.name}.` : undefined"
    />

    <section class="grid gap-3" aria-labelledby="rm-apps">
      <div>
        <h2 id="rm-apps" class="font-medium">Apps</h2>
        <p class="text-sm text-muted-foreground">Open-source apps with their own web address.</p>
      </div>
      <CatalogPicker :project="into || undefined" hide-intro />
    </section>

    <section class="grid gap-3" aria-labelledby="rm-services">
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
      <div v-else class="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
        <button
          v-for="t in services"
          :key="t.id"
          type="button"
          class="group flex items-center gap-3 rounded-lg border bg-card p-3 text-left transition-colors hover:bg-muted/40 focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
          @click="picked = t"
        >
          <ServiceIcon :role="templateRole(t)" :logo="t.id" size="sm" class="mt-0.5 self-start" />
          <span class="min-w-0 flex-1">
            <span class="flex items-baseline gap-1.5 text-sm font-medium">{{ t.name }} <span class="text-xs font-normal text-muted-foreground">{{ t.version }}</span></span>
            <span class="block text-xs text-muted-foreground">{{ t.description }}</span>
            <span class="mt-1.5 block text-[11px] text-muted-foreground tabular-nums">{{ categoryLabel(t.category) }} · Up to {{ formatMB(t.memory_mb) }} memory<template v-if="t.volume_mb">, {{ formatMB(t.volume_mb) }} storage</template></span>
          </span>
          <ChevronRight class="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
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
