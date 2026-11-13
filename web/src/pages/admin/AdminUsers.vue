<script setup lang="ts">
import { ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import QueryError from '@/components/QueryError.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { MoreHorizontal } from 'lucide-vue-next'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { keys } from '@/composables/queries'
import { useSession } from '@/composables/useSession'
import { api } from '@/lib/api'
import { formatMB } from '@/lib/format'
import { joinedText } from '@/lib/signups'
import type { AdminUser } from '@/lib/types'
import AdminUserDialogs from './AdminUserDialogs.vue'
import UserDomainsSelect from './UserDomainsSelect.vue'

// The server does not let an admin suspend their own account, so that row has no Suspend.
const { user: self } = useSession()

const users = useQuery({ queryKey: keys.adminUsers, queryFn: api.adminUsers, refetchInterval: 15_000 })
const dialogs = ref<InstanceType<typeof AdminUserDialogs> | null>(null)

// A row opens the person's page. Their name is the real link (keyboard, middle
// click); the domains and the menu at the end of the row do not navigate.
const router = useRouter()
const personRoute = (u: AdminUser) => ({ name: 'admin-person', params: { id: u.id } })
function onRow(e: MouseEvent, u: AdminUser) {
  if ((e.target as HTMLElement).closest('a, button, input, [role="combobox"], [data-row-actions]')) return
  void router.push(personRoute(u))
}
</script>

<template>
  <div class="grid gap-3">
    <QueryError v-if="users.isError.value" :error="users.error.value" @retry="users.refetch()" />
    <div v-else-if="users.isPending.value" class="grid gap-2"><Skeleton v-for="i in 3" :key="i" class="h-12 w-full" /></div>
    <div v-else class="overflow-x-auto rounded-xl border bg-card shadow-card">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Person</TableHead>
            <TableHead>Apps</TableHead>
            <TableHead>Memory for apps</TableHead>
            <TableHead class="hidden text-right md:table-cell">Deploys</TableHead>
            <TableHead class="hidden text-right md:table-cell">Build min</TableHead>
            <TableHead class="hidden text-right md:table-cell">Running h</TableHead>
            <TableHead class="w-0" />
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="u in users.data.value" :key="u.id" class="cursor-pointer" @click="onRow($event, u)">
            <TableCell>
              <p class="font-medium">
                <RouterLink :to="personRoute(u)" class="hover:underline">{{ u.name }}</RouterLink>
                <Badge v-if="u.role === 'admin'" variant="secondary" class="ml-1">Admin</Badge>
                <Badge v-if="u.status === 'suspended'" variant="destructive" class="ml-1">Suspended</Badge>
              </p>
              <p class="text-xs text-muted-foreground">{{ u.email }}</p>
              <p v-if="joinedText(u.joined)" class="text-xs text-muted-foreground">{{ joinedText(u.joined) }}</p>
            </TableCell>
            <TableCell class="tabular-nums">{{ u.app_count }} / {{ u.quota.apps }}<span class="block text-xs text-muted-foreground">{{ u.usage.apps_running }} running</span></TableCell>
            <TableCell class="tabular-nums">
              <span class="whitespace-nowrap">{{ formatMB(u.usage.memory_reserved_mb) }}<template v-if="!u.quota.overallocate"> / {{ formatMB(u.quota.memory_mb) }}</template></span>
              <Badge v-if="u.quota.overallocate" variant="secondary" class="ml-1.5" title="Memory and CPU are not checked for this person: not against a limit and not against what the server has free.">No limit</Badge>
            </TableCell>
            <TableCell class="hidden text-right tabular-nums md:table-cell">{{ u.usage.deploys_month }}</TableCell>
            <TableCell class="hidden text-right tabular-nums md:table-cell">{{ Math.round(u.usage.build_minutes_month) }}</TableCell>
            <TableCell class="hidden text-right tabular-nums md:table-cell">{{ Math.round(u.usage.running_hours_month) }}</TableCell>
            <TableCell data-row-actions class="cursor-default">
              <div class="flex items-center justify-end gap-1">
                <UserDomainsSelect :user="u" />
                <DropdownMenu>
                  <DropdownMenuTrigger as-child>
                    <Button size="icon-sm" variant="ghost" class="text-muted-foreground" :aria-label="`Actions for ${u.name}`"><MoreHorizontal /></Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem as-child><RouterLink :to="personRoute(u)">Open their page</RouterLink></DropdownMenuItem>
                    <DropdownMenuItem @select="dialogs?.edit(u)">Limits</DropdownMenuItem>
                    <DropdownMenuItem @select="dialogs?.resetPassword(u)">Reset password</DropdownMenuItem>
                    <DropdownMenuItem v-if="u.id !== self?.id" :variant="u.status === 'suspended' ? 'default' : 'destructive'" @select="dialogs?.toggleStatus(u)">
                      {{ u.status === 'suspended' ? 'Reactivate' : 'Suspend' }}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <AdminUserDialogs ref="dialogs" />
  </div>
</template>
