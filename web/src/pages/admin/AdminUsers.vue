<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import QueryError from '@/components/QueryError.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { formatMB } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import type { AdminUser } from '@/lib/types'
import UserDomainsSelect from './UserDomainsSelect.vue'

interface QuotaForm {
  apps: number
  memory_mb: number
  cpus: number
  deploys_per_hour: number
  domains_per_app: number
  volume_mb: number
}

const qc = useQueryClient()
const users = useQuery({ queryKey: keys.adminUsers, queryFn: api.adminUsers, refetchInterval: 15_000 })

const editing = ref<AdminUser | null>(null)
const editOpen = computed({
  get: () => editing.value !== null,
  set: (v) => {
    if (!v) editing.value = null
  },
})

const whole = (label: string, min = 0) => z.coerce.number({ invalid_type_error: `${label}: enter a number.` }).int(`${label}: use a whole number.`).min(min, `${label}: at least ${min}.`)
const form = useForm({
  validationSchema: toTypedSchema(
    z.object({
      apps: whole('Apps', 0),
      memory_mb: whole('Memory', 64),
      cpus: z.coerce.number({ invalid_type_error: 'CPU: enter a number.' }).min(0.1, 'CPU: at least 0.1.'),
      deploys_per_hour: whole('Deploys per hour', 1),
      domains_per_app: whole('Domains', 0),
      volume_mb: whole('Storage', 0),
    }),
  ),
  initialValues: { apps: 10, memory_mb: 2048, cpus: 2, deploys_per_hour: 20, domains_per_app: 5, volume_mb: 25600 },
})

const fields = [
  { name: 'apps', label: 'Apps' },
  { name: 'memory_mb', label: 'Memory in total (MB)' },
  { name: 'cpus', label: 'CPU in total' },
  { name: 'deploys_per_hour', label: 'Deploys per hour' },
  { name: 'domains_per_app', label: 'Custom domains per app' },
  { name: 'volume_mb', label: 'Storage in total (MB)' },
] as const

function edit(u: AdminUser) {
  editing.value = u
  form.resetForm({
    values: {
      apps: u.quota.apps,
      memory_mb: u.quota.memory_mb,
      cpus: u.quota.cpu_millis / 1000,
      deploys_per_hour: u.quota.deploys_per_hour,
      domains_per_app: u.quota.domains_per_app,
      volume_mb: u.quota.volume_mb,
    },
  })
}

const saveQuota = useMutation({
  mutationFn: (v: { id: string; values: QuotaForm }) =>
    api.updateUser(v.id, {
      quota: {
        apps: v.values.apps,
        memory_mb: v.values.memory_mb,
        cpu_millis: Math.round(v.values.cpus * 1000),
        deploys_per_hour: v.values.deploys_per_hour,
        domains_per_app: v.values.domains_per_app,
        volume_mb: v.values.volume_mb,
      },
    }),
  onSuccess: () => {
    toastOk('Limits saved')
    editing.value = null
    void qc.invalidateQueries({ queryKey: keys.adminUsers })
  },
  onError: (e) => toastError(e),
})
const submit = form.handleSubmit((values) => {
  if (editing.value && !saveQuota.isPending.value) saveQuota.mutate({ id: editing.value.id, values })
})

const toggling = ref<AdminUser | null>(null)
const toggleOpen = computed({
  get: () => toggling.value !== null,
  set: (v) => {
    if (!v) toggling.value = null
  },
})
const setStatus = useMutation({
  mutationFn: (u: AdminUser) => api.updateUser(u.id, { status: u.status === 'suspended' ? 'active' : 'suspended' }),
  onSuccess: (_r, u) => {
    toastOk(u.status === 'suspended' ? `${u.name} can log in again` : `${u.name} is suspended`)
    toggling.value = null
    void qc.invalidateQueries({ queryKey: keys.adminUsers })
    void qc.invalidateQueries({ queryKey: keys.adminApps }) // suspending stops the person's apps
  },
  onError: (e) => toastError(e),
})
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
            <TableHead>Memory reserved</TableHead>
            <TableHead class="hidden md:table-cell">This month</TableHead>
            <TableHead class="w-0" />
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="u in users.data.value" :key="u.id">
            <TableCell>
              <p class="font-medium">
                {{ u.name }}
                <Badge v-if="u.role === 'admin'" variant="secondary" class="ml-1">Admin</Badge>
                <Badge v-if="u.status === 'suspended'" variant="destructive" class="ml-1">Suspended</Badge>
              </p>
              <p class="text-xs text-muted-foreground">{{ u.email }}</p>
            </TableCell>
            <TableCell class="tabular-nums">{{ u.app_count }} / {{ u.quota.apps }}<span class="block text-xs text-muted-foreground">{{ u.usage.apps_running }} running</span></TableCell>
            <TableCell class="tabular-nums">{{ formatMB(u.usage.memory_reserved_mb) }} / {{ formatMB(u.quota.memory_mb) }}</TableCell>
            <TableCell class="hidden text-xs text-muted-foreground md:table-cell">
              {{ u.usage.deploys_month }} deploys<br />
              {{ Math.round(u.usage.build_minutes_month) }} build min<br />
              {{ Math.round(u.usage.running_hours_month) }} running h
            </TableCell>
            <TableCell>
              <div class="flex gap-1">
                <UserDomainsSelect :user="u" />
                <Button size="sm" variant="outline" :aria-label="`Limits for ${u.name}`" @click="edit(u)">Limits</Button>
                <Button size="sm" variant="ghost" :class="u.status !== 'suspended' && 'text-destructive'" :aria-label="`${u.status === 'suspended' ? 'Reactivate' : 'Suspend'} ${u.name}`" @click="toggling = u">
                  {{ u.status === 'suspended' ? 'Reactivate' : 'Suspend' }}
                </Button>
              </div>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <Dialog v-model:open="editOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Limits for {{ editing?.name }}</DialogTitle>
          <DialogDescription>These are the most this person can use.</DialogDescription>
        </DialogHeader>
        <form id="quota-form" class="grid gap-3 sm:grid-cols-2" novalidate @submit.prevent="submit">
          <FormField v-for="f in fields" :key="f.name" v-slot="{ componentField }" :name="f.name">
            <FormItem>
              <FormLabel>{{ f.label }}</FormLabel>
              <FormControl><Input type="number" step="any" inputmode="decimal" v-bind="componentField" /></FormControl>
              <FormMessage />
            </FormItem>
          </FormField>
        </form>
        <DialogFooter>
          <Button variant="outline" @click="editing = null">Cancel</Button>
          <Button type="submit" form="quota-form" :disabled="saveQuota.isPending.value">{{ saveQuota.isPending.value ? 'Saving...' : 'Save limits' }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      v-model:open="toggleOpen"
      :title="toggling?.status === 'suspended' ? 'Let this person back in?' : 'Suspend this person?'"
      :description="toggling?.status === 'suspended' ? `${toggling?.name} will be able to log in and use their tokens again.` : `${toggling?.name} can no longer log in or use tokens. Their apps are not deleted.`"
      :confirm-label="toggling?.status === 'suspended' ? 'Reactivate' : 'Suspend'"
      :destructive="toggling?.status !== 'suspended'"
      :loading="setStatus.isPending.value"
      @confirm="toggling && setStatus.mutate(toggling)"
    />
  </div>
</template>
