<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { Check, LoaderCircle, RefreshCw } from 'lucide-vue-next'
import CopyForAiButton from '@/components/CopyForAiButton.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { keys, liveMs } from '@/composables/queries'
import { liveBuildLogs } from '@/composables/useEvents'
import { useAppActions } from '@/composables/useAppActions'
import { api } from '@/lib/api'
import { plainHint } from '@/lib/errors'
import { deploymentStateInfo } from '@/lib/format'
import type { App } from '@/lib/types'

// The first deploy of an app, live: the step it is on, the build log, and the
// reason in plain words when it fails. Shown on the Overview while the app has
// no running version yet; with no deployment at all the slot is shown instead.
const props = defineProps<{ slug: string; app?: App }>()
const qc = useQueryClient()
const { redeploy } = useAppActions(() => props.slug)

const STEPS = [
  { state: 'queued', label: 'Waiting' },
  { state: 'fetching', label: 'Getting code' },
  { state: 'validating', label: 'Checking' },
  { state: 'building', label: 'Building' },
  { state: 'starting', label: 'Starting' },
  { state: 'health_check', label: 'Testing' },
  { state: 'swapping', label: 'Going live' },
]

const list = useQuery({
  queryKey: computed(() => keys.deployments(props.slug)),
  queryFn: () => api.deployments(props.slug),
  // Live events keep it fresh; without them, quick while something is going on
  // so the log and the steps still feel live.
  refetchInterval: (q) => {
    const s = q.state.data?.[0]?.state
    return liveMs(s && !['running', 'failed', 'superseded', 'cancelled'].includes(s) ? 1500 : 5000)
  },
})

const latest = computed(() => list.data.value?.[0] ?? null)
const step = computed(() => STEPS.findIndex((s) => s.state === latest.value?.state))
const failed = computed(() => latest.value?.state === 'failed')
const active = computed(() => !!latest.value && !['running', 'failed', 'superseded', 'cancelled'].includes(latest.value.state))
// While the build runs, the lines come live from the event stream; the polled
// log tail is the fallback, and what is shown once the deployment has ended.
const log = computed(() => {
  const live = latest.value && active.value ? liveBuildLogs.value[latest.value.id] : undefined
  return (live?.length ? live : (latest.value?.log_tail ?? [])).slice(-14)
})

const logEl = ref<HTMLElement | null>(null)
watch(
  log,
  async () => {
    await nextTick()
    if (logEl.value) logEl.value.scrollTop = logEl.value.scrollHeight
  },
)
// The app itself only shows a running version after the swap: ask for it at once.
watch(
  () => latest.value?.state,
  (s) => {
    if (s === 'running' || s === 'failed') void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
  },
)
</script>

<template>
  <Skeleton v-if="list.isPending.value" class="h-48 w-full" />
  <slot v-else-if="!latest" />
  <Card v-else>
    <CardHeader class="flex flex-row flex-wrap items-center justify-between gap-2">
      <CardTitle>{{ failed ? 'The deploy failed' : latest.state === 'running' ? 'Almost there' : 'Putting your app online' }}</CardTitle>
      <StatusBadge v-bind="deploymentStateInfo(latest.state)" />
    </CardHeader>
    <CardContent class="grid gap-4">
      <ol v-if="!failed" class="grid grid-cols-2 gap-x-4 gap-y-2 text-sm sm:grid-cols-4 lg:grid-cols-7" aria-label="Deploy steps">
        <li
          v-for="(s, i) in STEPS"
          :key="s.state"
          class="flex items-center gap-2"
          :class="i > step && latest.state !== 'running' ? 'text-muted-foreground' : ''"
          :aria-current="i === step ? 'step' : undefined"
        >
          <Check v-if="i < step || latest.state === 'running'" class="size-4 shrink-0 text-success" />
          <LoaderCircle v-else-if="i === step" class="size-4 shrink-0 animate-spin text-primary" />
          <span v-else class="size-4 shrink-0 rounded-full border border-dashed" aria-hidden="true" />
          <span :class="i === step && 'font-medium'">{{ s.label }}</span>
        </li>
      </ol>

      <div v-if="latest.error" class="rounded-lg bg-destructive/10 p-3 text-sm text-destructive" role="alert">
        <p class="font-medium">{{ latest.error.message }}</p>
        <p v-if="plainHint(latest.error.code, latest.error.hint)" class="mt-1">{{ plainHint(latest.error.code, latest.error.hint) }}</p>
      </div>

      <div v-if="log.length" class="grid gap-1.5">
        <p class="text-xs font-medium text-muted-foreground">{{ failed ? 'The last lines of the log' : 'Log' }}</p>
        <pre ref="logEl" class="max-h-56 overflow-auto rounded-lg border bg-background/60 p-3 font-mono text-xs whitespace-pre-wrap">{{ log.join('\n') }}</pre>
      </div>

      <div v-if="failed" class="flex flex-wrap items-center gap-2">
        <Button :disabled="redeploy.isPending.value" @click="redeploy.mutate()">
          <RefreshCw :class="redeploy.isPending.value && 'animate-spin'" /> Try again
        </Button>
        <CopyForAiButton v-if="app" :app="app" :deployment="latest" variant="outline" />
      </div>

      <p class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <RouterLink :to="{ name: 'app-logs', params: { slug }, query: { kind: 'build' } }" class="font-medium text-foreground hover:underline">Open the full build log</RouterLink>
        <RouterLink :to="{ name: 'app-deployments', params: { slug } }" class="font-medium text-foreground hover:underline">All deployments</RouterLink>
      </p>
    </CardContent>
  </Card>
</template>
