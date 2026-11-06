<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import PageHeader from '@/components/PageHeader.vue'
import { TabsContent } from '@/components/ui/tabs'
import { useQueryParam } from '@/composables/useQueryParam'
import AdminApps from './admin/AdminApps.vue'
import AdminAudit from './admin/AdminAudit.vue'
import AdminDomains from './admin/AdminDomains.vue'
import AdminGithub from './admin/AdminGithub.vue'
import AdminInvites from './admin/AdminInvites.vue'
import AdminServer from './admin/AdminServer.vue'
import AdminTabBar from './admin/AdminTabBar.vue'
import AdminUsers from './admin/AdminUsers.vue'
import { ADMIN_PAGE_TABS, type AdminTab } from './admin/adminTabs'

const route = useRoute()

// The open tab is in the address (?tab=apps), so a reload or a link keeps it.
// "database" has a wide page of its own (AdminDatabasePage) under the same tab bar.
const param = useQueryParam<AdminTab>('tab', 'users', ADMIN_PAGE_TABS)
// GitHub sends the admin back here with ?code=...&state=... after creating the app.
const tab = computed<AdminTab>(() => (route.query.code && !route.query.tab ? 'github' : param.value))
</script>

<template>
  <div class="grid gap-5">
    <PageHeader title="Admin" description="People, invites, GitHub and the health of the server." />
    <AdminTabBar :tab="tab">
      <TabsContent value="users" class="pt-6"><AdminUsers /></TabsContent>
      <TabsContent value="invites" class="pt-6"><AdminInvites /></TabsContent>
      <TabsContent value="server" class="pt-6"><AdminServer /></TabsContent>
      <TabsContent value="apps" class="pt-6"><AdminApps /></TabsContent>
      <TabsContent value="domains" class="pt-6"><AdminDomains /></TabsContent>
      <TabsContent value="github" class="pt-6"><AdminGithub /></TabsContent>
      <TabsContent value="audit" class="pt-6"><AdminAudit /></TabsContent>
    </AdminTabBar>
  </div>
</template>
