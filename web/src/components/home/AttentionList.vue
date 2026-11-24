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
  <section v-if="count" aria-label="Needs your attention" data-testid="attention" class="grid gap-2.5">
    <div
      v-for="f in failed"
      :key="f.app.id"
      class="flex flex-col gap-3 rounded-xl border border-destructive/30 bg-destructive/6 px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
    >
      <div class="flex min-w-0 flex-1 items-start gap-3 sm:items-center">
        <span class="flex size-8 shrink-0 items-center justify-center rounded-lg bg-destructive/15 text-destructive">
          <Hammer class="size-4" aria-hidden="true" />
        </span>
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-baseline gap-x-2 text-sm">
            <span class="font-semibold">{{ f.title }}</span>
            <span class="text-xs text-muted-foreground">
              · {{ f.app.status === 'crashed' ? 'crashed' : 'deploy failed' }}
              <template v-if="f.at"> <RelativeTime :date="f.at" /></template>
            </span>
          </div>
          <p class="line-clamp-2 text-xs text-muted-foreground">{{ f.reason }}</p>
        </div>
      </div>
      <div class="flex shrink-0 flex-wrap items-center gap-2">
        <Button size="sm" variant="outline" class="h-7 bg-card text-xs" as-child>
          <RouterLink :to="appRoute(f.app)">See what went wrong</RouterLink>
        </Button>
        <Button size="sm" variant="outline" class="h-7 bg-card text-xs text-primary-text" @click="askAi(f.app)">
          <Sparkles class="size-3.5" /> Ask AI to fix
        </Button>
      </div>
    </div>

    <div
      v-for="r in resources"
      :key="r.key"
      :class="cn(
        'flex flex-col gap-3 rounded-xl border px-4 py-3 sm:flex-row sm:items-center sm:justify-between',
        r.tone === 'bad' ? 'border-destructive/30 bg-destructive/6' : 'border-warning/30 bg-warning/6',
      )"
    >
      <div class="flex min-w-0 flex-1 items-start gap-3 sm:items-center">
        <span :class="cn('flex size-8 shrink-0 items-center justify-center rounded-lg', r.tone === 'bad' ? 'bg-destructive/15 text-destructive' : 'bg-warning/15 text-warning-text')">
          <TriangleAlert v-if="r.tone === 'bad'" class="size-4" aria-hidden="true" />
          <Gauge v-else class="size-4" aria-hidden="true" />
        </span>
        <div class="min-w-0 flex-1">
          <div class="text-sm font-semibold">{{ r.title }}</div>
          <p class="text-xs text-muted-foreground">{{ r.text }}</p>
        </div>
      </div>
      <Button v-if="r.action === 'storage'" size="sm" variant="outline" class="h-7 w-fit bg-card text-xs" @click="emit('storage')">
        See storage per app
      </Button>
    </div>
  </section>
</template>
