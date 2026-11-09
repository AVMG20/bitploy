<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { LogOut, Monitor, Smartphone } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import SettingsSection from '@/components/SettingsSection.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { forgetAllBrowsers } from '@/lib/push'
import { toastError, toastOk } from '@/lib/notify'
import { queryClient } from '@/lib/queryClient'

const router = useRouter()
const qc = useQueryClient()
const sessions = useQuery({ queryKey: keys.sessions, queryFn: api.sessions })
const revokeOne = useMutation({
  mutationFn: (id: string) => api.revokeSession(id),
  onSuccess: () => {
    toastOk('Device logged out')
    void qc.invalidateQueries({ queryKey: keys.sessions })
  },
  onError: (e) => toastError(e),
})
const everywhereOpen = ref(false)
const everywhere = useMutation({
  mutationFn: async () => {
    await api.revokeOtherSessions()
    await forgetAllBrowsers()
    await api.logout()
  },
  onSuccess: async () => {
    queryClient.clear()
    await router.replace({ name: 'login' })
  },
  onError: (e) => toastError(e),
})

function deviceName(ua: string): string {
  if (!ua) return 'Unknown device'
  const browser = /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Firefox\//.test(ua) ? 'Firefox' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
  const os = /Windows/.test(ua) ? 'Windows' : /Android/.test(ua) ? 'Android' : /iPhone|iPad/.test(ua) ? 'iOS' : /Mac OS X/.test(ua) ? 'macOS' : /Linux/.test(ua) ? 'Linux' : ''
  return os ? `${browser} on ${os}` : browser
}
const otherCount = computed(() => (sessions.data.value ?? []).filter((s) => !s.current).length)
</script>

<template>
  <SettingsSection title="Where you are logged in" description="If you see a device you do not know, log it out and change your password.">
    <template #aside>
      <Button variant="outline" size="sm" @click="everywhereOpen = true"><LogOut /> Log out everywhere</Button>
    </template>

    <QueryError v-if="sessions.isError.value" :error="sessions.error.value" @retry="sessions.refetch()" />
    <Skeleton v-else-if="sessions.isPending.value" class="h-24 w-full" />
    <Card v-else class="gap-0 py-0">
      <ul class="divide-y">
        <li v-for="s in sessions.data.value" :key="s.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3">
          <div class="flex size-8 items-center justify-center rounded-lg border bg-muted/50"><component :is="/Mobile|Android|iPhone/.test(s.user_agent) ? Smartphone : Monitor" class="size-4 text-muted-foreground" /></div>
          <div class="min-w-0">
            <p class="text-sm font-medium">
              {{ deviceName(s.user_agent) }}
              <Badge v-if="s.current" variant="secondary" class="ml-1">This device</Badge>
            </p>
            <p class="text-xs text-muted-foreground">{{ s.ip }} &middot; last active <RelativeTime :date="s.last_seen_at" /> &middot; logged in <RelativeTime :date="s.created_at" /></p>
          </div>
          <Button v-if="!s.current" class="ml-auto" size="sm" variant="ghost" :disabled="revokeOne.isPending.value" :aria-label="`Log out ${deviceName(s.user_agent)}`" @click="revokeOne.mutate(s.id)">Log out</Button>
        </li>
      </ul>
    </Card>

    <ConfirmDialog
      v-model:open="everywhereOpen"
      title="Log out everywhere?"
      :description="`You will be logged out on this device${otherCount ? ` and ${otherCount} other${otherCount === 1 ? '' : 's'}` : ''}. Browser notifications stop on all your devices. API tokens keep working.`"
      confirm-label="Log out everywhere"
      :loading="everywhere.isPending.value"
      @confirm="everywhere.mutate()"
    />
  </SettingsSection>
</template>
