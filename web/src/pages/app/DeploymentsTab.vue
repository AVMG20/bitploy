<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { History, Undo2 } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { keys, REFRESH_MS, useApp } from '@/composables/queries'
import { api } from '@/lib/api'
import { channelLabel, deploymentStateInfo, describeSource, formatDuration, shortSha } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import type { Deployment } from '@/lib/types'

const props = defineProps<{ slug: string }>()
const qc = useQueryClient()
const app = useApp(() => props.slug)
const list = useQuery({
  queryKey: computed(() => keys.deployments(props.slug)),
  queryFn: () => api.deployments(props.slug),
  refetchInterval: REFRESH_MS,
})

const target = ref<Deployment | null>(null)
const confirmOpen = computed({
  get: () => target.value !== null,
  set: (v) => {
    if (!v) target.value = null
  },
})

const rollback = useMutation({
  mutationFn: (d: Deployment) => api.rollback(props.slug, d.id),
  onSuccess: (_r, d) => {
    toastOk(`Going back to version #${d.seq}`, 'Your app keeps running while the old version starts.')
    target.value = null
    void qc.invalidateQueries({ queryKey: keys.deployments(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
  },
  onError: (e) => toastError(e),
})

function canRollback(d: Deployment): boolean {
  return d.state === 'superseded' && !!d.image && d.id !== app.data.value?.current_deployment?.id
}
</script>

<template>
  <div class="grid gap-3">
    <QueryError v-if="list.isError.value" :error="list.error.value" @retry="list.refetch()" />
    <div v-else-if="list.isPending.value" class="grid gap-2">
      <Skeleton v-for="i in 3" :key="i" class="h-20 w-full" />
    </div>
    <EmptyState
      v-else-if="!list.data.value?.length"
      :icon="History"
      title="No deployments yet."
      description="Tell your AI assistant to deploy to this app and every version will show up here."
    >
      <Button as-child variant="outline"><RouterLink to="/connect">Open Connect AI</RouterLink></Button>
    </EmptyState>
    <template v-else>
      <article v-for="d in list.data.value" :key="d.id" class="grid gap-2 rounded-xl border p-4">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
          <span class="font-semibold">#{{ d.seq }}</span>
          <StatusBadge v-bind="deploymentStateInfo(d.state)" />
          <span v-if="d.id === app.data.value?.current_deployment?.id" class="text-xs font-medium text-emerald-700 dark:text-emerald-400">Current version</span>
          <span class="text-sm text-muted-foreground"><RelativeTime :date="d.queued_at" /></span>
          <span class="text-sm text-muted-foreground">Took {{ formatDuration(d.duration_seconds) }}</span>
          <Button v-if="canRollback(d)" class="ml-auto" size="sm" variant="outline" @click="target = d">
            <Undo2 /> Go back to this version
          </Button>
        </div>
        <div class="flex flex-wrap gap-x-4 gap-y-1 text-sm text-muted-foreground">
          <span>Via {{ channelLabel(d.channel) }}</span>
          <span>{{ describeSource(d, app.data.value) }}</span>
          <span v-if="d.commit_sha">
            <span class="font-mono">{{ shortSha(d.commit_sha) }}</span>
            <template v-if="d.commit_message"> {{ d.commit_message }}</template>
          </span>
        </div>
        <div v-if="d.error" class="rounded-lg bg-red-500/10 p-3 text-sm text-red-800 dark:text-red-300">
          <p class="font-medium">{{ d.error.message }}</p>
          <p v-if="d.error.hint" class="mt-1">{{ d.error.hint }}</p>
          <details v-if="d.log_tail?.length" class="mt-2">
            <summary class="cursor-pointer text-xs underline">Show the last log lines</summary>
            <pre class="mt-2 max-h-60 overflow-auto rounded bg-background/60 p-2 font-mono text-xs whitespace-pre-wrap">{{ d.log_tail.join('\n') }}</pre>
          </details>
        </div>
        <ul v-if="d.warnings?.length" class="grid gap-1 text-sm">
          <li
            v-for="(w, i) in d.warnings"
            :key="i"
            :class="w.severity === 'info' ? 'text-muted-foreground' : 'text-amber-700 dark:text-amber-400'"
          >
            {{ w.message }}<template v-if="w.hint">&nbsp;{{ w.hint }}</template>
          </li>
        </ul>
      </article>
    </template>

    <ConfirmDialog
      v-model:open="confirmOpen"
      title="Go back to this version?"
      :description="`Your app will run version #${target?.seq ?? ''} again. The version running now stays available to switch back to.`"
      confirm-label="Go back"
      :loading="rollback.isPending.value"
      @confirm="target && rollback.mutate(target)"
    />
  </div>
</template>
