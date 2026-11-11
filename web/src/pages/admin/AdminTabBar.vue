<script setup lang="ts">
// The Admin tab bar. The Database tab opens a page of its own
// (/admin/database), and the Server tab's disk card opens /admin/disk; both
// show this same bar so the admin keeps their place.
import { nextTick, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import type { AdminTab } from './adminTabs'

// page: this bar sits on a page of its own (not AdminPage), under tab.
const props = defineProps<{ tab: AdminTab; page?: boolean }>()
const router = useRouter()

const tabs: { value: AdminTab; label: string }[] = [
  { value: 'users', label: 'People' },
  { value: 'invites', label: 'Invites' },
  { value: 'signups', label: 'Signups' },
  { value: 'server', label: 'Server' },
  { value: 'jobs', label: 'Jobs' },
  { value: 'apps', label: 'All apps' },
  { value: 'domains', label: 'Domains' },
  { value: 'github', label: 'GitHub' },
  { value: 'audit', label: 'Activity log' },
  { value: 'database', label: 'Database' },
]

function onTab(v: string | number) {
  if (v === 'database') void router.push({ name: 'admin-database' })
  else if (props.tab === 'database' || props.page) void router.push({ name: 'admin', query: { tab: v === 'users' ? undefined : String(v) } })
  else void router.replace({ name: 'admin', query: { tab: v === 'users' ? undefined : String(v) } })
}

// The open tab's own page: its trigger leads back to the tab.
function onTrigger(v: AdminTab) {
  if (props.page && v === props.tab && v !== 'database') void router.push({ name: 'admin', query: { tab: v === 'users' ? undefined : v } })
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
        <TabsTrigger v-for="t in tabs" :key="t.value" :value="t.value" @click="onTrigger(t.value)">{{ t.label }}</TabsTrigger>
      </TabsList>
    </div>
    <slot />
  </Tabs>
</template>
