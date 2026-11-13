<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyButton from '@/components/CopyButton.vue'
import QueryError from '@/components/QueryError.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { MoreHorizontal } from 'lucide-vue-next'
import { Switch } from '@/components/ui/switch'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { keys } from '@/composables/queries'
import { useSession } from '@/composables/useSession'
import { api } from '@/lib/api'
import { formatMB } from '@/lib/format'
import { gbToMB, mbToGB } from '@/lib/quota'
import { toastError, toastOk } from '@/lib/notify'
import { joinedText } from '@/lib/signups'
import type { AdminUser, PasswordReset } from '@/lib/types'
import UserDomainsSelect from './UserDomainsSelect.vue'

// The server does not let an admin suspend their own account, so that row has no Suspend.
const { user: self } = useSession()

// Memory and storage are entered in GB, the unit the tables show; the API takes MB.
interface QuotaForm {
  apps: number
  memory_gb: number
  cpus: number
  domains_per_app: number
  volume_gb: number
  overallocate: boolean
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
      memory_gb: z.coerce.number({ invalid_type_error: 'Memory: enter a number.' }).min(0, 'Memory: at least 0.'),
      cpus: z.coerce.number({ invalid_type_error: 'CPU cores: enter a number.' }).min(0, 'CPU cores: at least 0.'),
      domains_per_app: whole('Domains', 0),
      volume_gb: z.coerce.number({ invalid_type_error: 'Storage: enter a number.' }).min(0, 'Storage: at least 0.'),
      overallocate: z.boolean(),
    }),
  ),
  initialValues: { apps: 10, memory_gb: 2, cpus: 2, domains_per_app: 5, volume_gb: 25, overallocate: false },
})

// Memory and CPU come last, next to the switch that turns their check off.
const fields = [
  { name: 'apps', label: 'Apps', hint: '0: no apps of their own; they can still work in projects shared with them.' },
  { name: 'domains_per_app', label: 'Custom domains per app', hint: '0: none, only the bitploy address.' },
  { name: 'volume_gb', label: 'Storage in total (GB)', hint: '0: no limit.' },
  { name: 'memory_gb', label: 'Memory in total (GB)', hint: '0: no apps of their own.' },
  { name: 'cpus', label: 'CPU cores in total', hint: '0.5 is half of one core; 0: no apps of their own.' },
] as const
const unchecked = (name: string) => !!form.values.overallocate && (name === 'memory_gb' || name === 'cpus')

function edit(u: AdminUser) {
  editing.value = u
  form.resetForm({
    values: {
      apps: u.quota.apps,
      memory_gb: mbToGB(u.quota.memory_mb),
      cpus: u.quota.cpu_millis / 1000,
      domains_per_app: u.quota.domains_per_app,
      volume_gb: mbToGB(u.quota.volume_mb),
      overallocate: !!u.quota.overallocate,
    },
  })
}

const saveQuota = useMutation({
  // The server replaces every value. Memory and storage left as the form showed them keep their exact MB.
  mutationFn: (v: { user: AdminUser; values: QuotaForm }) =>
    api.updateUser(v.user.id, {
      quota: {
        apps: v.values.apps,
        memory_mb: gbToMB(v.values.memory_gb, v.user.quota.memory_mb),
        cpu_millis: Math.round(v.values.cpus * 1000),
        domains_per_app: v.values.domains_per_app,
        volume_mb: gbToMB(v.values.volume_gb, v.user.quota.volume_mb),
        overallocate: v.values.overallocate,
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
  if (editing.value && !saveQuota.isPending.value) saveQuota.mutate({ user: editing.value, values })
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

// A one-time link to pick a new password. There is no email: the admin sends
// it. A new link makes the older unused ones of that person stop working.
const resetting = ref<AdminUser | null>(null)
const resetLink = ref<PasswordReset | null>(null)
const resetOpen = computed({
  get: () => resetting.value !== null || resetLink.value !== null,
  set: (v) => {
    if (!v) {
      resetting.value = null
      resetLink.value = null
    }
  },
})
const createReset = useMutation({
  mutationFn: (u: AdminUser) => api.createPasswordReset(u.id),
  onSuccess: (r) => {
    resetting.value = null
    resetLink.value = r
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
            <TableHead>Memory for apps</TableHead>
            <TableHead class="hidden text-right md:table-cell">Deploys</TableHead>
            <TableHead class="hidden text-right md:table-cell">Build min</TableHead>
            <TableHead class="hidden text-right md:table-cell">Running h</TableHead>
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
            <TableCell>
              <div class="flex items-center justify-end gap-1">
                <UserDomainsSelect :user="u" />
                <DropdownMenu>
                  <DropdownMenuTrigger as-child>
                    <Button size="icon-sm" variant="ghost" class="text-muted-foreground" :aria-label="`Actions for ${u.name}`"><MoreHorizontal /></Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem @select="edit(u)">Limits</DropdownMenuItem>
                    <DropdownMenuItem @select="resetting = u">Reset password</DropdownMenuItem>
                    <DropdownMenuItem v-if="u.id !== self?.id" :variant="u.status === 'suspended' ? 'default' : 'destructive'" @select="toggling = u">
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

    <Dialog v-model:open="editOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Limits for {{ editing?.name }}</DialogTitle>
          <DialogDescription>These are the most this person can use.</DialogDescription>
        </DialogHeader>
        <form id="quota-form" class="grid items-start gap-3 sm:grid-cols-2" novalidate @submit.prevent="submit">
          <FormField v-for="f in fields" :key="f.name" v-slot="{ componentField }" :name="f.name">
            <FormItem>
              <FormLabel :class="unchecked(f.name) && 'text-muted-foreground'">{{ f.label }}</FormLabel>
              <FormControl><Input type="number" step="any" inputmode="decimal" v-bind="componentField" :disabled="unchecked(f.name)" /></FormControl>
              <FormDescription v-if="unchecked(f.name)">Not checked: overallocating is on.</FormDescription>
              <FormDescription v-else-if="f.hint">{{ f.hint }}</FormDescription>
              <FormMessage />
            </FormItem>
          </FormField>
          <!-- For the people who run the server: their apps may ask for more than the quota and the server have. -->
          <FormField v-slot="{ value, handleChange }" name="overallocate" type="checkbox">
            <FormItem class="flex items-start justify-between gap-3 rounded-lg border p-3 sm:col-span-2">
              <div class="grid gap-1">
                <FormLabel>Allow overallocating memory and CPU</FormLabel>
                <FormDescription>Memory and CPU are not checked: not against these limits and not against what the server has free. Meant for admins who know what the server can take.</FormDescription>
              </div>
              <FormControl><Switch :model-value="value" aria-label="Allow overallocating memory and CPU" @update:model-value="handleChange" /></FormControl>
            </FormItem>
          </FormField>
        </form>
        <DialogFooter>
          <Button variant="outline" @click="editing = null">Cancel</Button>
          <Button type="submit" form="quota-form" :disabled="saveQuota.isPending.value">{{ saveQuota.isPending.value ? 'Saving...' : 'Save limits' }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="resetOpen">
      <DialogContent>
        <template v-if="resetLink">
          <DialogHeader>
            <DialogTitle>Reset link for {{ resetLink.user.name }}</DialogTitle>
            <DialogDescription>Send this link to {{ resetLink.user.name }} yourself; it works once and expires in 24 hours.</DialogDescription>
          </DialogHeader>
          <div class="flex items-center gap-1 rounded-lg border bg-muted/40 p-3">
            <code class="min-w-0 flex-1 font-mono text-xs break-all">{{ resetLink.url }}</code>
            <CopyButton :value="resetLink.url" what="Reset link copied" label="Copy" variant="secondary" />
          </div>
          <p class="text-xs text-muted-foreground">It is shown only now. Using it signs {{ resetLink.user.name }} out everywhere; their API tokens keep working.</p>
          <DialogFooter>
            <Button @click="resetOpen = false">Done</Button>
          </DialogFooter>
        </template>
        <template v-else>
          <DialogHeader>
            <DialogTitle>Reset the password of {{ resetting?.name }}?</DialogTitle>
            <DialogDescription>
              This makes a link where {{ resetting?.name }} ({{ resetting?.email }}) picks a new password. Their current password keeps working until the link is used. A link you made for them before stops working.
            </DialogDescription>
          </DialogHeader>
          <p v-if="resetting?.status === 'suspended'" class="text-sm text-muted-foreground">This person is suspended: the link only works once you reactivate them.</p>
          <DialogFooter>
            <Button variant="outline" @click="resetOpen = false">Cancel</Button>
            <Button :disabled="createReset.isPending.value" @click="resetting && createReset.mutate(resetting)">{{ createReset.isPending.value ? 'Making link...' : 'Make reset link' }}</Button>
          </DialogFooter>
        </template>
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
