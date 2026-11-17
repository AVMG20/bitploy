<script setup lang="ts">
// "Needs your attention" on Home: apps that failed or crashed, then limits that
// are close or passed. Shows nothing when everything is fine.
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { Gauge, Hammer, Sparkles, TriangleAlert } from 'lucide-vue-next'
import RelativeTime from '@/components/RelativeTime.vue'
import { Button } from '@/components/ui/button'
import { appRoute } from '@/lib/appLinks'
import { buildFailureText } from '@/lib/copyForAi'
import { copyTextLater } from '@/lib/notify'
import type { App } from '@/lib/types'
import { cn } from '@/lib/utils'
import type { Attention } from './useServerUsage'

const props = defineProps<{ apps: readonly App[]; resources: readonly Attention[] }>()
const emit = defineEmits<{ storage: [] }>()

interface Failed {
  app: App
  title: string
  reason: string
  at: string | null | undefined
}

/** The reason in plain words: the deployment's own message first, then what the app reports. */
function reasonOf(a: App): string {
  const d = a.current_deployment
  const msg = a.status_message || (d?.state === 'failed' ? d.error?.message : '') || d?.error?.message
  if (msg) return msg.split('\n')[0]!
  return a.status === 'crashed' ? 'The app stopped by itself.' : 'The last deploy failed.'
}

const failed = computed<Failed[]>(() =>
  props.apps
    .filter((a) => a.status === 'failed' || a.status === 'crashed' || a.current_deployment?.state === 'failed')
    .map((a) => ({
      app: a,
      title: a.name,
      reason: reasonOf(a),
      at: a.current_deployment?.finished_at,
    })),
)
const count = computed(() => failed.value.length + props.resources.length)

/** The failure text names the app, its slug and the error, with the end of the log. */
function askAi(a: App) {
  void copyTextLater(async () => {
    try {
      return await buildFailureText(a, null)
    } catch {
      return `My app "${a.name}" (slug ${a.slug}) has a problem: ${reasonOf(a)}`
    }
  }, 'Copied. Paste it into your AI assistant.')
}
</script>

<template>
  <section v-if="count" aria-labelledby="attention-title" data-testid="attention">
    <div class="mb-3 flex items-center gap-2">
      <h2 id="attention-title" class="text-sm font-semibold">Needs your attention</h2>
      <span class="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-destructive/10 px-1.5 text-xs font-medium text-destructive tabular-nums">{{ count }}</span>
    </div>
    <div class="grid gap-3 md:grid-cols-2">
      <div v-for="f in failed" :key="f.app.id" class="flex gap-3.5 rounded-xl border border-destructive/30 bg-card p-4 shadow-card">
        <span class="flex size-9 shrink-0 items-center justify-center rounded-lg bg-destructive/10 text-destructive"><Hammer class="size-4" aria-hidden="true" /></span>
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-x-2">
            <span class="font-medium">{{ f.title }}</span>
            <span class="text-xs text-muted-foreground">· {{ f.app.status === 'crashed' ? 'crashed' : 'deploy failed' }} <RelativeTime v-if="f.at" :date="f.at" /></span>
          </div>
          <p class="mt-1 line-clamp-3 text-sm text-muted-foreground">{{ f.reason }}</p>
          <div class="mt-3 flex flex-wrap gap-2">
            <Button size="sm" as-child><RouterLink :to="appRoute(f.app)">See what went wrong</RouterLink></Button>
            <Button size="sm" variant="outline" @click="askAi(f.app)"><Sparkles /> Ask AI to fix</Button>
          </div>
        </div>
      </div>
      <div v-for="r in resources" :key="r.key" :class="cn('flex gap-3.5 rounded-xl border bg-card p-4 shadow-card', r.tone === 'bad' ? 'border-destructive/30' : 'border-warning/30')">
        <span :class="cn('flex size-9 shrink-0 items-center justify-center rounded-lg', r.tone === 'bad' ? 'bg-destructive/10 text-destructive' : 'bg-warning/10 text-warning-text')">
          <TriangleAlert v-if="r.tone === 'bad'" class="size-4" aria-hidden="true" /><Gauge v-else class="size-4" aria-hidden="true" />
        </span>
        <div class="min-w-0 flex-1">
          <div class="font-medium">{{ r.title }}</div>
          <p class="mt-1 text-sm text-muted-foreground">{{ r.text }}</p>
          <div v-if="r.action === 'storage'" class="mt-3">
            <Button size="sm" variant="outline" @click="emit('storage')">See storage per app</Button>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
