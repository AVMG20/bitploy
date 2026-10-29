<script setup lang="ts">
import { ref } from 'vue'
import PageHeader from '@/components/PageHeader.vue'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import AdminApps from './admin/AdminApps.vue'
import AdminAudit from './admin/AdminAudit.vue'
import AdminInvites from './admin/AdminInvites.vue'
import AdminServer from './admin/AdminServer.vue'
import AdminUsers from './admin/AdminUsers.vue'

const tab = ref('users')
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Admin" description="People, invites and the health of the server." />
    <Tabs v-model="tab" class="min-w-0">
      <div class="-mx-1 overflow-x-auto px-1">
        <TabsList>
          <TabsTrigger value="users">People</TabsTrigger>
          <TabsTrigger value="invites">Invites</TabsTrigger>
          <TabsTrigger value="server">Server</TabsTrigger>
          <TabsTrigger value="apps">All apps</TabsTrigger>
          <TabsTrigger value="audit">Activity log</TabsTrigger>
        </TabsList>
      </div>
      <TabsContent value="users" class="mt-4"><AdminUsers /></TabsContent>
      <TabsContent value="invites" class="mt-4"><AdminInvites /></TabsContent>
      <TabsContent value="server" class="mt-4"><AdminServer /></TabsContent>
      <TabsContent value="apps" class="mt-4"><AdminApps /></TabsContent>
      <TabsContent value="audit" class="mt-4"><AdminAudit /></TabsContent>
    </Tabs>
  </div>
</template>
