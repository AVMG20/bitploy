<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PageHeader from '@/components/PageHeader.vue'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useQueryParam } from '@/composables/useQueryParam'
import AdminApps from './admin/AdminApps.vue'
import AdminAudit from './admin/AdminAudit.vue'
import AdminGithub from './admin/AdminGithub.vue'
import AdminInvites from './admin/AdminInvites.vue'
import AdminServer from './admin/AdminServer.vue'
import AdminUsers from './admin/AdminUsers.vue'

const route = useRoute()
const router = useRouter()

// The open tab is in the address (?tab=apps), so a reload or a link keeps it.
// "database" is not a tab here: bitploy's own database has a wide page of its own.
const tabs = [
  { value: 'users', label: 'People' },
  { value: 'invites', label: 'Invites' },
  { value: 'server', label: 'Server' },
  { value: 'apps', label: 'All apps' },
  { value: 'github', label: 'GitHub' },
  { value: 'audit', label: 'Activity log' },
  { value: 'database', label: 'Database' },
] as const
type Tab = (typeof tabs)[number]['value']
const param = useQueryParam<Tab>('tab', 'users', tabs.map((t) => t.value))
// GitHub sends the admin back here with ?code=...&state=... after creating the app.
const tab = computed(() => (route.query.code && !route.query.tab ? 'github' : param.value))
function onTab(v: string | number) {
  if (v === 'database') void router.push({ name: 'admin-database' })
  else void router.replace({ query: { tab: v === 'users' ? undefined : String(v) } })
}
</script>

<template>
  <div class="grid gap-5">
    <PageHeader title="Admin" description="People, invites, GitHub and the health of the server." />
    <Tabs :model-value="tab" class="min-w-0 gap-0" @update:model-value="onTab">
      <div class="overflow-x-auto overflow-y-hidden border-b">
        <TabsList variant="line" class="-ml-1.5">
          <TabsTrigger v-for="t in tabs" :key="t.value" :value="t.value">{{ t.label }}</TabsTrigger>
        </TabsList>
      </div>
      <TabsContent value="users" class="pt-6"><AdminUsers /></TabsContent>
      <TabsContent value="invites" class="pt-6"><AdminInvites /></TabsContent>
      <TabsContent value="server" class="pt-6"><AdminServer /></TabsContent>
      <TabsContent value="apps" class="pt-6"><AdminApps /></TabsContent>
      <TabsContent value="github" class="pt-6"><AdminGithub /></TabsContent>
      <TabsContent value="audit" class="pt-6"><AdminAudit /></TabsContent>
    </Tabs>
  </div>
</template>
