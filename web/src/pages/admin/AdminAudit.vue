<script setup lang="ts">
import { appPath } from '@/lib/appLinks'
import { computed } from 'vue'
import { useInfiniteQuery, useQuery } from '@tanstack/vue-query'
import ActivityLog from '@/components/ActivityLog.vue'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { auditDetails } from '@/lib/audit'
import type { AuditRow } from '@/lib/audit'
import type { AuditEntry } from '@/lib/types'

const audit = useInfiniteQuery({
  queryKey: keys.adminAudit,
  queryFn: ({ pageParam }) => api.audit(pageParam),
  initialPageParam: undefined as string | undefined,
  getNextPageParam: (last) => last.next_cursor || undefined,
})
const users = useQuery({ queryKey: keys.adminUsers, queryFn: api.adminUsers })
const apps = useQuery({ queryKey: keys.adminApps, queryFn: api.adminApps })
const names = computed(() => new Map((users.data.value ?? []).map((u) => [u.id, u.name || u.email])))
const appsById = computed(() => new Map((apps.data.value ?? []).map((a) => [a.id, a])))
const entries = computed(() => audit.data.value?.pages.flatMap((p) => p.items ?? []) ?? [])

function who(e: AuditEntry): string {
  return e.user_id ? (names.value.get(e.user_id) ?? 'A removed user') : 'bitploy'
}
/** What the entry is about, in words, and where to go for it. */
function target(e: AuditEntry): AuditRow['target'] {
  if (!e.target_id) return null
  if (e.target_type === 'app') {
    const a = appsById.value.get(e.target_id)
    if (a) return { text: a.name, to: appPath(a.slug) }
    const slug = e.details?.slug ?? e.details?.old_slug
    return { text: typeof slug === 'string' ? slug : 'a deleted app' }
  }
  if (e.target_type === 'user') return { text: names.value.get(e.target_id) ?? 'a user' }
  if (e.target_type === 'domain' && typeof e.details?.hostname === 'string') return { text: e.details.hostname }
  return null
}
const rows = computed<AuditRow[]>(() =>
  entries.value.map((e) => ({
    id: e.id,
    ts: e.ts,
    who: who(e),
    channel: e.channel,
    action: e.action,
    target: target(e),
    details: auditDetails(e.details),
    ip: e.ip,
  })),
)
</script>

<template>
  <ActivityLog
    :rows="rows"
    :pending="audit.isPending.value"
    :error="audit.isError.value ? audit.error.value : null"
    :fetching="audit.isFetching.value"
    :has-more="audit.hasNextPage.value"
    :fetching-more="audit.isFetchingNextPage.value"
    where-column
    empty-description="Every change made in bitploy shows up here for 90 days."
    @refresh="audit.refetch()"
    @more="audit.fetchNextPage()"
  />
</template>
