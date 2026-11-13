<script setup lang="ts">
// What happened to your apps, projects and account: by you, the people you
// share a project with, an admin or bitploy itself (GET /activity). Admins
// see the same here; the whole log is Admin > Activity log.
import { computed } from 'vue'
import { useInfiniteQuery } from '@tanstack/vue-query'
import ActivityLog from '@/components/ActivityLog.vue'
import PageHeader from '@/components/PageHeader.vue'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { activityRow } from '@/lib/audit'

const activity = useInfiniteQuery({
  queryKey: keys.activity,
  queryFn: ({ pageParam }) => api.activity(pageParam),
  initialPageParam: undefined as string | undefined,
  getNextPageParam: (last) => last.next_cursor || undefined,
})
const rows = computed(() => (activity.data.value?.pages.flatMap((p) => p.items ?? []) ?? []).map(activityRow))
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Activity" description="What happened to your apps, your projects and your account in the last 90 days, and who did it." />
    <ActivityLog
      :rows="rows"
      :pending="activity.isPending.value"
      :error="activity.isError.value ? activity.error.value : null"
      :fetching="activity.isFetching.value"
      :has-more="activity.hasNextPage.value"
      :fetching-more="activity.isFetchingNextPage.value"
      target-column
      :group-labels="{ admin: 'Your account' }"
      empty-title="Nothing happened yet."
      empty-description="Changes to your apps, your projects and your account show up here: who made them, and when."
      @refresh="activity.refetch()"
      @more="activity.fetchNextPage()"
    />
  </div>
</template>
