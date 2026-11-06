<script setup lang="ts">
// The Admin tab bar. The Database tab opens a wide page of its own
// (/admin/database), which shows this same bar so the admin keeps their place.
import { nextTick, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import type { AdminTab } from './adminTabs'

const props = defineProps<{ tab: AdminTab }>()
const router = useRouter()

const tabs: { value: AdminTab; label: string }[] = [
  { value: 'users', label: 'People' },
  { value: 'invites', label: 'Invites' },
  { value: 'server', label: 'Server' },
  { value: 'apps', label: 'All apps' },
  { value: 'domains', label: 'Domains' },
  { value: 'github', label: 'GitHub' },
  { value: 'audit', label: 'Activity log' },
  { value: 'database', label: 'Database' },
]

function onTab(v: string | number) {
  if (v === 'database') void router.push({ name: 'admin-database' })
  else if (props.tab === 'database') void router.push({ name: 'admin', query: { tab: v === 'users' ? undefined : String(v) } })
  else void router.replace({ name: 'admin', query: { tab: v === 'users' ? undefined : String(v) } })
}

// On a phone the bar scrolls sideways: keep the open tab in view.
const bar = ref<HTMLElement | null>(null)
function showActive() {
  void nextTick(() => {
    const el = bar.value?.querySelector<HTMLElement>('[data-state="active"]')
    el?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  })
}
onMounted(showActive)
watch(() => props.tab, showActive)
</script>

<template>
  <Tabs :model-value="tab" class="min-w-0 gap-0" @update:model-value="onTab">
    <div ref="bar" class="overflow-x-auto overflow-y-hidden border-b [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
      <TabsList variant="line" class="-ml-1.5">
        <TabsTrigger v-for="t in tabs" :key="t.value" :value="t.value">{{ t.label }}</TabsTrigger>
      </TabsList>
    </div>
    <slot />
  </Tabs>
</template>
