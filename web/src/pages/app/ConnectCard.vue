<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowRight } from 'lucide-vue-next'
import CopyButton from '@/components/CopyButton.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useBackups, useProject, useProjectEnv } from '@/composables/queries'
import { formatBytes } from '@/lib/format'
import { aliasOf, internalHostOf, projectIdOf, projectLinks, referenceText, usedBy } from '@/lib/projects'
import type { App } from '@/lib/types'

// How the other apps of the project reach a database or other ready-made
// service: the reference to paste into their variables, its address inside the
// project, who already uses it and when it was last backed up.
const props = defineProps<{ app: App }>()

const reference = computed(() => (props.app.main_var ? referenceText(aliasOf(props.app), props.app.main_var) : ''))
const host = computed(() => internalHostOf(props.app))

const { project } = useProject(() => projectIdOf(props.app))
const siblings = computed(() => project.value?.apps ?? [])
const env = useProjectEnv(siblings)
const users = computed(() => usedBy(props.app, siblings.value, projectLinks(siblings.value, env.value)))

const backups = useBackups(() => props.app.slug, () => props.app.backups_supported)
const lastBackup = computed(() => backups.data.value?.items.find((b) => b.status === 'ready') ?? null)
</script>

<template>
  <Card>
    <CardHeader class="flex flex-row items-center justify-between gap-2">
      <CardTitle>Connect</CardTitle>
      <span v-if="app.template_version" class="text-xs text-muted-foreground">Version {{ app.template_version }}</span>
    </CardHeader>
    <CardContent class="grid gap-3 text-sm">
      <dl class="grid grid-cols-[5.5rem_minmax(0,1fr)] items-center gap-x-4 gap-y-2.5">
        <template v-if="reference">
          <dt class="text-muted-foreground">Reference</dt>
          <dd class="flex min-w-0 items-center gap-1">
            <code class="truncate font-mono text-[13px]" data-testid="connect-reference">{{ reference }}</code>
            <CopyButton :value="reference" what="Reference copied" label="Copy reference" icon class="size-6 shrink-0 text-muted-foreground" />
          </dd>
        </template>
        <template v-if="host">
          <dt class="text-muted-foreground">Address</dt>
          <dd class="flex min-w-0 items-center gap-1">
            <code class="truncate font-mono text-[13px]" data-testid="connect-host">{{ host }}</code>
            <CopyButton :value="host" what="Address copied" label="Copy address" icon class="size-6 shrink-0 text-muted-foreground" />
          </dd>
        </template>
        <dt class="text-muted-foreground">Used by</dt>
        <dd class="flex min-w-0 flex-wrap items-center gap-1.5" data-testid="connect-used-by">
          <RouterLink
            v-for="a in users"
            :key="a.id"
            :to="{ name: 'app', params: { slug: a.slug } }"
            class="inline-flex h-6 items-center rounded-md border bg-muted/40 px-2 font-mono text-xs transition-colors hover:bg-muted"
          >{{ aliasOf(a) }}</RouterLink>
          <span v-if="!users.length" class="text-muted-foreground">No app yet</span>
        </dd>
      </dl>
      <div v-if="app.backups_supported" class="flex items-center justify-between gap-3 border-t pt-3 text-xs text-muted-foreground">
        <span v-if="lastBackup">Last backup <RelativeTime :date="lastBackup.created_at" />, {{ formatBytes(lastBackup.size_bytes) }}</span>
        <span v-else-if="backups.isPending.value">&nbsp;</span>
        <span v-else>No backup yet</span>
        <RouterLink :to="{ name: 'app-storage', params: { slug: app.slug } }" class="inline-flex shrink-0 items-center gap-1 font-medium text-foreground hover:underline">
          Backups <ArrowRight class="size-3.5" />
        </RouterLink>
      </div>
    </CardContent>
  </Card>
</template>
