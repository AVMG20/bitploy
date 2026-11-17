<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowRight, CircleCheck, CircleDashed, CircleX, Hammer, RefreshCw } from 'lucide-vue-next'
import LogLines from '@/components/LogLines.vue'
import { Button } from '@/components/ui/button'
import { useAppActions } from '@/composables/useAppActions'
import { isPortError } from '@/lib/runSettings'
import { isMulti } from '@/lib/services'
import { versionLabel } from '@/lib/format'
import type { App, Deployment } from '@/lib/types'
import AskAiButton from './AskAiButton.vue'
import DeployFailure from './DeployFailure.vue'

// The newest deploy failed but an older version still serves: say that first
// (the site is up), show which step failed and the few lines that matter,
// then what to do. With nothing live, the Overview shows DeployProgress instead.
const props = defineProps<{ slug: string; app: App; failed: Deployment; live: Deployment }>()
const { redeploy } = useAppActions(() => props.slug)

const STEPS = ['Got the code', 'Checked it', 'Build', 'Start', 'Go live']

// A failed deployment only says "failed"; its error says where it stopped.
const failedStep = computed(() => {
  const code = props.failed.error?.code ?? ''
  if (/git|fetch|clone|source|upload/.test(code)) return 0
  if (isPortError(code) || /dockerfile|validat|compose|no_/.test(code)) return 1
  if (code === 'build_failed') return 2
  if (props.failed.error) return 3
  return props.failed.log_tail?.some((l) => /^#\d+ /.test(l)) ? 2 : 3
})
const verb = computed(() => (failedStep.value <= 2 ? 'build' : 'start'))
const online = computed(() => props.app.status === 'running')

// The few lines that matter: the end of the log.
const lines = computed(() => (props.failed.log_tail ?? []).slice(-6))

// The setting that most likely fixes it.
const fix = computed(() => {
  const code = props.failed.error?.code ?? ''
  if (isPortError(code)) return { label: 'Set the port', to: { name: 'app-settings', hash: '#run' } }
  if (code === 'before_deploy_failed' || code === 'before_deploy_timeout' || code === 'after_deploy_failed') return { label: 'Change deploy commands', to: { name: 'app-settings', hash: '#deploy-commands' } }
  if (code === 'build_failed') return { label: 'Change the build settings', to: { name: 'app-settings', hash: '#build' } }
  if (code === 'app_crashed' || code === 'unhealthy') return { label: 'Check the variables', to: { name: 'app-variables' } }
  return { label: 'Open the settings', to: { name: 'app-settings' } }
})
</script>

<template>
  <section class="overflow-hidden rounded-xl border border-destructive/35 bg-card shadow-card" data-testid="failed-deploy">
    <div class="flex flex-wrap items-start justify-between gap-4 bg-gradient-to-b from-destructive/10 to-transparent p-5">
      <div class="flex min-w-0 gap-3.5">
        <span class="flex size-10 shrink-0 items-center justify-center rounded-lg bg-destructive/10 text-destructive"><Hammer class="size-5" aria-hidden="true" /></span>
        <div class="min-w-0">
          <h2 class="text-base font-semibold">{{ versionLabel(failed.seq) }} did not {{ verb }}</h2>
          <p class="mt-0.5 text-sm text-muted-foreground">
            <template v-if="online">Your {{ app.type === 'web' ? 'site' : 'app' }} is still online and runs {{ versionLabel(live.seq).toLowerCase() }}. Nothing your visitors see has changed.</template>
            <template v-else>{{ versionLabel(live.seq) }} is the last version that worked.</template>
          </p>
        </div>
      </div>
      <div class="flex flex-wrap gap-2">
        <Button variant="outline" :disabled="redeploy.isPending.value" @click="redeploy.mutate()">
          <RefreshCw :class="redeploy.isPending.value && 'animate-spin'" /> Try again
        </Button>
        <AskAiButton :app="app" :deployment="failed" />
      </div>
    </div>

    <ol class="flex flex-wrap gap-x-6 gap-y-2 px-5 pb-1 text-sm" aria-label="Deploy steps">
      <li v-for="(s, i) in STEPS" :key="s" class="inline-flex items-center gap-1.5" :class="i === failedStep ? 'font-medium text-destructive' : i > failedStep ? 'text-muted-foreground' : ''">
        <CircleCheck v-if="i < failedStep" class="size-4 text-success" aria-hidden="true" />
        <CircleX v-else-if="i === failedStep" class="size-4" aria-hidden="true" />
        <CircleDashed v-else class="size-4" aria-hidden="true" />
        {{ s }}
      </li>
    </ol>

    <div class="grid gap-4 p-5 pt-4 lg:grid-cols-[minmax(0,1fr)_20rem]">
      <div v-if="lines.length" class="min-w-0 rounded-lg border bg-background p-3 font-mono text-xs leading-relaxed">
        <div class="overflow-x-auto"><LogLines :lines="lines" /></div>
        <RouterLink :to="{ name: 'app-logs', params: { slug }, query: { kind: 'build' } }" class="mt-2 inline-flex items-center gap-1 font-sans text-xs text-muted-foreground hover:text-foreground">
          Full build log <ArrowRight class="size-3.5" aria-hidden="true" />
        </RouterLink>
      </div>
      <div class="min-w-0 self-start">
        <DeployFailure v-if="failed.error" :error="failed.error" :log-tail="failed.log_tail" :multi="isMulti(app)">
          <template #actions>
            <Button as-child size="sm" variant="outline">
              <RouterLink :to="{ ...fix.to, params: { slug } }">{{ fix.label }} <ArrowRight /></RouterLink>
            </Button>
          </template>
        </DeployFailure>
        <Button v-else as-child size="sm" variant="outline">
          <RouterLink :to="{ ...fix.to, params: { slug } }">{{ fix.label }} <ArrowRight /></RouterLink>
        </Button>
      </div>
    </div>
  </section>
</template>
