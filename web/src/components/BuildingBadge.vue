<script setup lang="ts">
// Next to an app's status: the new version that builds or waits, linking to
// its Deployments tab. Nothing while no deployment is in progress.
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { Clock, LoaderCircle } from 'lucide-vue-next'
import { useActiveBuilds } from '@/composables/queries'
import { deploymentStateInfo } from '@/lib/format'

const props = defineProps<{ appId: string }>()
const { buildOf } = useActiveBuilds()
const build = computed(() => buildOf(props.appId))
const text = computed(() => {
  const b = build.value
  if (!b) return ''
  if (b.state === 'queued') return `Version ${b.seq} waits${b.position && b.position > 1 ? ` · ${b.position - 1} ahead` : b.position === 1 ? ' · next up' : ''}`
  return `${deploymentStateInfo(b.state).label} version ${b.seq}`
})
const to = computed(() => (build.value ? { name: 'app-deployments', params: { projectId: build.value.app.project_id || build.value.app.id, slug: build.value.app.slug } } : '/'))
</script>

<template>
  <RouterLink
    v-if="build"
    :to="to"
    class="inline-flex h-5.5 items-center gap-1.5 rounded-md border px-2 text-xs font-medium whitespace-nowrap text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
    data-testid="building-badge"
  >
    <Clock v-if="build.state === 'queued'" class="size-3" aria-hidden="true" />
    <LoaderCircle v-else class="size-3 animate-spin motion-reduce:animate-none" aria-hidden="true" />
    {{ text }}
  </RouterLink>
</template>
