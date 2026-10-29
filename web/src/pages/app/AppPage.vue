<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { ArrowLeft, ExternalLink } from 'lucide-vue-next'
import AppStatusBadge from '@/components/AppStatusBadge.vue'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useApp } from '@/composables/queries'
import { ApiError } from '@/lib/api'

const props = defineProps<{ slug: string }>()
const route = useRoute()
const app = useApp(() => props.slug)

const tabs = [
  { name: 'app', label: 'Overview' },
  { name: 'app-deployments', label: 'Deployments' },
  { name: 'app-logs', label: 'Logs' },
  { name: 'app-domains', label: 'Domains' },
  { name: 'app-settings', label: 'Settings' },
]
const current = computed(() => String(route.name))
const notFound = computed(() => app.error.value instanceof ApiError && app.error.value.status === 404)
</script>

<template>
  <div class="grid gap-5">
    <RouterLink to="/" class="inline-flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
      <ArrowLeft class="size-4" /> All apps
    </RouterLink>

    <EmptyState v-if="notFound" title="We cannot find that app." description="It may have been deleted, or the address has a typo.">
      <Button as-child><RouterLink to="/">Back to your apps</RouterLink></Button>
    </EmptyState>
    <QueryError v-else-if="app.isError.value && !app.data.value" :error="app.error.value" @retry="app.refetch()" />

    <template v-else>
      <div class="flex flex-wrap items-center gap-3">
        <template v-if="app.data.value">
          <h1 class="min-w-0 truncate text-2xl font-semibold tracking-tight">{{ app.data.value.name }}</h1>
          <AppStatusBadge :status="app.data.value.status" />
          <Button v-if="app.data.value.type === 'web'" as-child variant="outline" size="sm" class="ml-auto">
            <a :href="app.data.value.url" target="_blank" rel="noopener noreferrer">
              Open app <ExternalLink />
            </a>
          </Button>
        </template>
        <Skeleton v-else class="h-8 w-64" />
      </div>

      <Tabs :model-value="current">
        <div class="-mx-1 overflow-x-auto px-1">
          <TabsList>
            <TabsTrigger v-for="t in tabs" :key="t.name" :value="t.name" as-child>
              <RouterLink :to="{ name: t.name, params: { slug } }">{{ t.label }}</RouterLink>
            </TabsTrigger>
          </TabsList>
        </div>
      </Tabs>

      <RouterView />
    </template>
  </div>
</template>
