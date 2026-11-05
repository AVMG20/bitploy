<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowRight, Cable, Pencil } from 'lucide-vue-next'
import EmptyState from '@/components/EmptyState.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { openAdd } from '@/composables/useAddToProject'
import { useProjectData } from '@/composables/useProjectData'
import { appRoute, projectRoute } from '@/lib/appLinks'
import { aliasOf, envReferences, referenceText } from '@/lib/projects'
import type { App } from '@/lib/types'

// Every variable of the project in one place: which app reads which variable
// of which other app, then each app's own variables. Values stay masked;
// references to other apps are not secret and are shown as written.
const props = defineProps<{ projectId: string }>()
const { apps, env, links, roleOf } = useProjectData(() => props.projectId)
const byId = computed(() => new Map(apps.value.map((a) => [a.id, a])))
const connections = computed(() =>
  links.value.flatMap((l) => {
    const from = byId.value.get(l.from)
    const to = byId.value.get(l.to)
    return from && to ? [{ key: `${l.from}>${l.to}`, from, to, vars: l.vars }] : []
  }),
)
const panel = (a: App) => projectRoute(props.projectId, { app: a.slug })
</script>

<template>
  <EmptyState v-if="!apps.length" title="No variables yet." description="Add an app to the project, then give it variables.">
    <Button @click="openAdd(projectId)">Add to the project</Button>
  </EmptyState>
  <div v-else class="grid gap-6 xl:grid-cols-[minmax(0,24rem)_minmax(0,1fr)] xl:items-start">
    <Card class="xl:sticky xl:top-16">
      <CardHeader>
        <CardTitle class="flex items-center gap-2"><Cable class="size-4 text-muted-foreground" /> Connections</CardTitle>
        <CardDescription>
          An app reads another app's variable through a reference like
          <span class="font-mono text-foreground/80">{{ referenceText('postgres', 'DATABASE_URL') }}</span>. It keeps working when the value changes.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ul v-if="connections.length" class="divide-y rounded-lg border text-sm" data-testid="project-connections">
          <li v-for="c in connections" :key="c.key" class="grid gap-1 px-3 py-2.5">
            <span class="flex min-w-0 items-center gap-1.5">
              <RouterLink :to="panel(c.from)" class="truncate rounded-md border bg-muted/50 px-1.5 py-0.5 font-mono text-xs hover:border-foreground/30">{{ aliasOf(c.from) }}</RouterLink>
              <ArrowRight class="size-3.5 shrink-0 text-muted-foreground" aria-label="reads from" />
              <RouterLink :to="panel(c.to)" class="truncate rounded-md border bg-muted/50 px-1.5 py-0.5 font-mono text-xs hover:border-foreground/30">{{ aliasOf(c.to) }}</RouterLink>
            </span>
            <span class="truncate font-mono text-xs text-muted-foreground">{{ c.vars.join(', ') }}</span>
          </li>
        </ul>
        <p v-else class="text-sm text-muted-foreground">No app reads another app's variables yet. Add a database and pick the apps that should get its address.</p>
      </CardContent>
    </Card>

    <div class="grid gap-4">
      <Card v-for="a in apps" :key="a.id" class="gap-0 py-0">
        <div class="flex min-w-0 items-center gap-3 border-b px-4 py-3">
          <ServiceIcon :role="roleOf(a).key" size="sm" />
          <RouterLink :to="panel(a)" class="truncate font-medium hover:underline">{{ a.name }}</RouterLink>
          <span v-if="aliasOf(a) !== a.name" class="truncate font-mono text-xs text-muted-foreground">{{ aliasOf(a) }}</span>
          <span v-if="env[a.slug]" class="text-xs text-muted-foreground">{{ env[a.slug]!.length === 1 ? '1 variable' : `${env[a.slug]!.length} variables` }}</span>
          <Button variant="ghost" size="sm" class="ml-auto" as-child>
            <RouterLink :to="appRoute(a, 'app-variables')"><Pencil /> Edit</RouterLink>
          </Button>
        </div>
        <div v-if="!env[a.slug]" class="grid gap-2 p-4"><Skeleton class="h-4 w-2/3" /><Skeleton class="h-4 w-1/2" /></div>
        <p v-else-if="!env[a.slug]!.length" class="px-4 py-3 text-sm text-muted-foreground">No variables.</p>
        <ul v-else class="divide-y text-sm">
          <li v-for="v in env[a.slug]" :key="`${v.service}:${v.name}`" class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2">
            <span class="font-mono font-medium break-all">{{ v.name }}</span>
            <span v-if="v.service" class="rounded border px-1 text-[11px] text-muted-foreground">{{ v.service }}</span>
            <span class="ml-auto flex min-w-0 flex-wrap items-center justify-end gap-1">
              <template v-if="envReferences(v).length">
                <span
                  v-for="r in envReferences(v)"
                  :key="r.raw"
                  class="inline-flex items-center gap-1 rounded-md border border-primary/25 bg-primary/10 px-1.5 py-0.5 font-mono text-[11px] text-foreground/90"
                  :title="`The value of ${r.variable} in ${r.app}`"
                ><Cable class="size-3 text-primary" /> {{ r.app }}.{{ r.variable }}</span>
              </template>
              <span v-else class="truncate font-mono text-xs text-muted-foreground">{{ v.masked }}</span>
            </span>
          </li>
        </ul>
      </Card>
    </div>
  </div>
</template>
