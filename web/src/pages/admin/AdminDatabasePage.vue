<script setup lang="ts">
// bitploy's own database, under the Admin header and tab bar like the other Admin tabs.
import PageHeader from '@/components/PageHeader.vue'
import DatabasePage from '../app/database/DatabasePage.vue'
import AdminTabBar from './AdminTabBar.vue'
</script>

<template>
  <div class="grid min-w-0 gap-5">
    <PageHeader title="Admin" description="People, invites, GitHub and the health of the server." />
    <AdminTabBar tab="database" />
    <DatabasePage admin class="pt-1" />
  </div>
</template>
