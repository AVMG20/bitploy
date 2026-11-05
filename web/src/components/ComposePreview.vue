<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Globe, HardDrive, Info, Loader2, Lock, TriangleAlert } from 'lucide-vue-next'
import ServiceIcon from '@/components/ServiceIcon.vue'
import { Progress } from '@/components/ui/progress'
import { useMe } from '@/composables/queries'
import { api } from '@/lib/api'
import { composeIssueText } from '@/lib/errors'
import { formatCPUMillis, formatMB, pct } from '@/lib/format'
import { listWords, serviceRole, serviceSource, sortServices } from '@/lib/services'
import type { ComposeCheck, ComposeIssue, ComposeService } from '@/lib/types'
import { cn } from '@/lib/utils'

// What bitploy would run from a compose file, before anything is created: each
// service in plain words, which one gets the public address, what data is
// kept, and whether it fits in what is left of the memory and storage.
const props = defineProps<{
  /** The compose file's text. */
  text: string
  /** Where it comes from, like compose.yaml in your upload. */
  from?: string
  /** The app's address, for the public service. */
  address: string
}>()
/** The outcome: undefined while checking, null when the server cannot check compose files. */
const check = defineModel<ComposeCheck | null | undefined>('check', { default: undefined })

const me = useMe()
const state = ref<'checking' | 'done' | 'unsupported' | 'failed'>('checking')
const failure = ref('')
let seq = 0
let timer: number | null = null

async function run(text: string) {
  const mine = ++seq
  state.value = 'checking'
  check.value = undefined
  try {
    const r = await api.validateCompose(text)
    if (mine !== seq) return
    check.value = r
    state.value = r === null ? 'unsupported' : 'done'
  } catch (e) {
    if (mine !== seq) return
    failure.value = e instanceof Error ? e.message : ''
    // Treat it like a server that cannot check: deploying says what is wrong.
    check.value = null
    state.value = 'failed'
  }
}
watch(
  () => props.text,
  (t) => {
    if (timer !== null) clearTimeout(timer)
    if (!t.trim()) {
      seq++
      check.value = undefined
      state.value = 'checking'
      return
    }
    timer = window.setTimeout(() => void run(t), 400)
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  seq++
  if (timer !== null) clearTimeout(timer)
})

const c = computed(() => (state.value === 'done' ? check.value : null))
const services = computed(() => sortServices(c.value?.services ?? []))
const errors = computed(() => c.value?.errors ?? [])
const warnings = computed<ComposeIssue[]>(() => c.value?.warnings ?? [])
const volumes = computed(() => c.value?.volumes ?? [])

function deps(s: ComposeService): string[] {
  return (s.depends_on ?? []).filter(Boolean)
}
/** The volumes a service mounts, with the size and path from the file's volume list. */
function mounts(s: ComposeService): { name: string; size: number | null; path: string }[] {
  const names = new Set([...(s.volumes ?? []), ...volumes.value.filter((v) => v.service === s.name).map((v) => v.name)])
  return [...names].filter(Boolean).map((name) => {
    const v = volumes.value.find((x) => x.name === name)
    return { name, path: v?.mount_path ?? '', size: v?.size_mb ?? null }
  })
}

// ---------------------------------------------------------------- does it fit
const memoryTotal = computed(() => c.value?.memory_mb_total ?? services.value.reduce((n, s) => n + s.memory_mb, 0))
const cpuTotal = computed(() => c.value?.cpu_millis_total ?? services.value.reduce((n, s) => n + s.cpu_millis, 0))
const memoryLeft = computed(() => {
  if (c.value?.quota) return c.value.quota.memory_mb_left
  const m = me.data.value
  return m ? Math.max(0, m.quota.memory_mb - m.usage.memory_reserved_mb) : null
})
const cpuLeft = computed(() => {
  if (c.value?.quota) return c.value.quota.cpu_millis_left
  const m = me.data.value
  return m ? Math.max(0, m.quota.cpu_millis - m.usage.cpu_reserved_millis) : null
})
const storageTotal = computed(() => c.value?.volume_mb_total ?? volumes.value.reduce((n, v) => n + v.size_mb, 0))
const storageLeft = computed(() => {
  if (c.value?.quota) return c.value.quota.storage_mb_left
  const m = me.data.value
  return m ? Math.max(0, m.quota.volume_mb - m.usage.files_mb - m.usage.backups_mb - m.usage.volume_reserved_mb) : null
})
const memoryOver = computed(() => memoryLeft.value !== null && memoryTotal.value > memoryLeft.value)
// The server's own sentence when it says the file does not fit.
const quotaMessage = computed(() => (c.value?.quota && !c.value.quota.fits ? c.value.quota.message : ''))
const cpuOver = computed(() => cpuLeft.value !== null && cpuTotal.value > cpuLeft.value)
const storageOver = computed(() => storageLeft.value !== null && storageTotal.value > storageLeft.value)
const noPublic = computed(() => services.value.length > 0 && !services.value.some((s) => s.public))
</script>

<template>
  <section class="grid gap-3 rounded-xl border bg-muted/20 p-4" aria-live="polite" aria-labelledby="compose-preview-title">
    <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
      <h3 id="compose-preview-title" class="text-sm font-medium">What bitploy will run</h3>
      <p v-if="from" class="text-xs text-muted-foreground">Read from <span class="font-mono">{{ from }}</span> in your files</p>
    </div>

    <p v-if="state === 'checking'" class="flex items-center gap-2 py-2 text-sm text-muted-foreground">
      <Loader2 class="size-4 animate-spin" /> Reading the compose file...
    </p>

    <p v-else-if="state === 'unsupported' || state === 'failed'" class="flex items-start gap-2 text-sm text-muted-foreground">
      <Info class="mt-0.5 size-4 shrink-0" />
      <span>
        {{ state === 'unsupported' ? 'This server cannot show a preview of compose files.' : `The preview did not load${failure ? `: ${failure}` : '.'}` }}
        You can still deploy; if something in the file is not allowed, the deploy says what and where.
      </span>
    </p>

    <template v-else-if="c">
      <ul v-if="errors.length" class="grid gap-2" data-testid="compose-errors">
        <li v-for="(e, i) in errors" :key="i" class="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm">
          <TriangleAlert class="mt-0.5 size-4 shrink-0 text-destructive" />
          <span class="min-w-0">
            <span class="font-medium text-destructive">{{ composeIssueText(e).title }}</span>
            <span v-if="composeIssueText(e).detail" class="block text-foreground/90">{{ composeIssueText(e).detail }}</span>
            <span v-if="composeIssueText(e).hint" class="block text-muted-foreground">{{ composeIssueText(e).hint }}</span>
          </span>
        </li>
      </ul>

      <ul v-if="services.length" class="divide-y overflow-hidden rounded-lg border bg-card">
        <li v-for="s in services" :key="s.name" class="grid gap-1.5 px-3 py-2.5">
          <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            <ServiceIcon :role="serviceRole(s).key" size="sm" />
            <div class="min-w-0 flex-1">
              <p class="flex min-w-0 items-baseline gap-2 text-sm">
                <span class="font-medium">{{ serviceRole(s).label }}</span>
                <span class="truncate font-mono text-xs text-muted-foreground">{{ s.name }}</span>
              </p>
              <p class="truncate text-xs text-muted-foreground" :title="serviceSource(s)">
                {{ serviceRole(s).product || serviceSource(s) }}<template v-if="serviceRole(s).product && s.image"> · <span class="font-mono">{{ s.image }}</span></template>
              </p>
            </div>
            <span
              v-if="s.public"
              class="inline-flex max-w-full items-center gap-1 rounded-md border border-primary/30 bg-primary/10 px-1.5 py-0.5 text-xs text-foreground"
              title="Visitors open this service at the app's address."
            >
              <Globe class="size-3 shrink-0 text-primary" /> <span class="truncate font-mono">{{ address }}</span>
            </span>
            <span v-else class="inline-flex items-center gap-1 text-xs text-muted-foreground" :title="s.port ? `Only the other services reach it, at ${s.name}:${s.port}.` : 'It does not listen for requests.'">
              <Lock class="size-3 shrink-0" /> Private
            </span>
            <span class="w-24 shrink-0 text-right text-xs text-muted-foreground tabular-nums">{{ formatMB(s.memory_mb) }}</span>
          </div>
          <p v-if="deps(s).length || mounts(s).length" class="flex flex-wrap gap-x-4 gap-y-1 pl-10 text-xs text-muted-foreground">
            <span v-if="deps(s).length">Talks to <span class="font-mono text-foreground/80">{{ listWords(deps(s)) }}</span></span>
            <span v-for="m in mounts(s)" :key="m.name" class="inline-flex items-center gap-1">
              <HardDrive class="size-3" />
              <span>Saves files in <span class="font-mono text-foreground/80">{{ m.name }}</span>{{ m.size !== null ? `, ${formatMB(m.size)}` : '' }}</span>
            </span>
          </p>
        </li>
      </ul>

      <p v-if="noPublic && !errors.length" class="text-xs text-muted-foreground">
        No service gets a web address: this app runs in the background. Give the website a <span class="font-mono">ports:</span> entry to put it online.
      </p>

      <div v-if="services.length" class="grid gap-3 sm:grid-cols-2">
        <div class="grid gap-1.5">
          <p class="flex items-baseline justify-between gap-2 text-xs">
            <span class="text-muted-foreground">Memory reserved</span>
            <span class="tabular-nums" :class="memoryOver && 'font-medium text-destructive'">
              {{ formatMB(memoryTotal) }}<span v-if="memoryLeft !== null" class="text-muted-foreground"> of {{ formatMB(memoryLeft) }} left</span>
            </span>
          </p>
          <Progress :class="cn(memoryOver ? '[&>[data-slot=progress-indicator]]:bg-destructive' : '[&>[data-slot=progress-indicator]]:bg-success')" :model-value="memoryLeft ? Math.min(100, pct(memoryTotal, memoryLeft)) : 0" />
          <p class="text-xs text-muted-foreground" :class="cpuOver && 'text-destructive'">
            and {{ formatCPUMillis(cpuTotal) }}<template v-if="cpuLeft !== null"> of {{ formatCPUMillis(cpuLeft) }} left</template>
          </p>
        </div>
        <div class="grid content-start gap-1.5">
          <p class="flex items-baseline justify-between gap-2 text-xs">
            <span class="text-muted-foreground">Saved files</span>
            <span class="tabular-nums" :class="storageOver && 'font-medium text-destructive'">
              <template v-if="volumes.length">{{ formatMB(storageTotal) }}<span v-if="storageLeft !== null" class="text-muted-foreground"> of {{ formatMB(storageLeft) }} left</span></template>
              <span v-else class="text-muted-foreground">None</span>
            </span>
          </p>
          <Progress :class="cn(storageOver ? '[&>[data-slot=progress-indicator]]:bg-destructive' : '[&>[data-slot=progress-indicator]]:bg-success')" :model-value="storageLeft && volumes.length ? Math.min(100, pct(storageTotal, storageLeft)) : 0" />
          <p class="text-xs text-muted-foreground">{{ volumes.length ? 'Kept between deploys and backed up.' : 'Nothing is kept between deploys.' }}</p>
        </div>
      </div>
      <p v-if="quotaMessage || memoryOver || cpuOver || storageOver" class="text-xs text-destructive">
        {{ quotaMessage || 'This does not fit in what is left of your allowance.' }} Lower the limits in the file (mem_limit, cpus, volume size), or delete another app first.
      </p>

      <ul v-if="warnings.length" class="grid gap-1 text-xs text-[color-mix(in_oklch,var(--warning)_75%,var(--foreground))]">
        <li v-for="(w, i) in warnings" :key="i" class="flex items-start gap-1.5">
          <TriangleAlert class="mt-0.5 size-3 shrink-0" />
          <span>{{ w.code ? [composeIssueText(w).title, composeIssueText(w).detail].filter(Boolean).join(' ') : w.message }}</span>
        </li>
      </ul>
    </template>
  </section>
</template>
