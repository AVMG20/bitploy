<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import PageHeader from '@/components/PageHeader.vue'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

const route = useRoute()
const tabs = [
  { name: 'settings', label: 'Account' },
  { name: 'settings-tokens', label: 'API tokens' },
  { name: 'settings-notifications', label: 'Notifications' },
  { name: 'settings-github', label: 'GitHub' },
  { name: 'settings-sessions', label: 'Sessions' },
]
const current = computed(() => String(route.name))
</script>

<template>
  <div class="grid gap-5">
    <PageHeader title="Settings" description="Your account, the tokens your AI assistants use, your notifications and your connections." />
    <Tabs :model-value="current" class="min-w-0">
      <div class="overflow-x-auto overflow-y-hidden border-b">
        <TabsList variant="line" class="-ml-1.5">
          <TabsTrigger v-for="t in tabs" :key="t.name" :value="t.name" as-child>
            <RouterLink :to="{ name: t.name }">{{ t.label }}</RouterLink>
          </TabsTrigger>
        </TabsList>
      </div>
    </Tabs>
    <div class="pt-2"><RouterView /></div>
  </div>
</template>
