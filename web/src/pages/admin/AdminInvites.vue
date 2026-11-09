<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Plus } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyButton from '@/components/CopyButton.vue'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import type { AdminProjectInvite, Invite } from '@/lib/types'

const qc = useQueryClient()
const invites = useQuery({ queryKey: keys.adminInvites, queryFn: api.invites, refetchInterval: 15_000 })
// An invite does not keep who used it; an invite for an email address was used by the account with that address.
const users = useQuery({ queryKey: keys.adminUsers, queryFn: api.adminUsers })
const userByEmail = computed(() => new Map((users.data.value ?? []).map((u) => [u.email.toLowerCase(), u])))
function usedBy(i: Invite): string {
  if (!i.email) return ''
  const u = userByEmail.value.get(i.email.toLowerCase())
  return u ? u.name || u.email : ''
}
const fresh = ref<Invite | null>(null)

const { handleSubmit, resetForm } = useForm({
  validationSchema: toTypedSchema(
    z.object({
      email: z.string().trim().email('That does not look like an email address.').or(z.literal('')),
      role: z.enum(['user', 'admin']),
    }),
  ),
  initialValues: { email: '', role: 'user' as const },
})

const create = useMutation({
  mutationFn: (v: { email: string; role: string }) => api.createInvite({ email: v.email || undefined, role: v.role }),
  onSuccess: (inv) => {
    fresh.value = inv
    resetForm()
    toastOk('Invite created', 'Send the link to your colleague. It works once.')
    void qc.invalidateQueries({ queryKey: keys.adminInvites })
  },
  onError: (e) => toastError(e),
})
const submit = handleSubmit((v) => create.mutate(v))

const toRevoke = ref<Invite | null>(null)
const revokeOpen = computed({
  get: () => toRevoke.value !== null,
  set: (v) => {
    if (!v) toRevoke.value = null
  },
})
const revoke = useMutation({
  mutationFn: (id: string) => api.revokeInvite(id),
  onSuccess: () => {
    toastOk('Invite revoked')
    toRevoke.value = null
    void qc.invalidateQueries({ queryKey: keys.adminInvites })
  },
  onError: (e) => toastError(e),
})

// The open invites people made to their projects: an admin sees them all and can revoke any.
const projectInvites = useQuery({ queryKey: keys.adminProjectInvites, queryFn: api.adminProjectInvites, refetchInterval: 15_000 })
const toRevokeProject = ref<AdminProjectInvite | null>(null)
const revokeProjectOpen = computed({
  get: () => toRevokeProject.value !== null,
  set: (v) => {
    if (!v) toRevokeProject.value = null
  },
})
const revokeProject = useMutation({
  mutationFn: (id: string) => api.revokeAdminProjectInvite(id),
  onSuccess: () => {
    toastOk('Project invite revoked')
    toRevokeProject.value = null
    void qc.invalidateQueries({ queryKey: keys.adminProjectInvites })
  },
  onError: (e) => toastError(e),
})

/** What became of the invite, and when: used (and by whom), expired, or waiting until it expires. */
function state(i: Invite): { label: string; tone: string; date: string } {
  if (i.used_at) return { label: 'Used', tone: 'text-muted-foreground', date: i.used_at }
  if (new Date(i.expires_at).getTime() < Date.now()) return { label: 'Expired', tone: 'text-muted-foreground', date: i.expires_at }
  return { label: 'Waiting', tone: 'text-success', date: i.expires_at }
}
</script>

<template>
  <div class="grid gap-4">
    <Card>
      <CardHeader>
        <CardTitle>Invite someone</CardTitle>
        <CardDescription>You get a single-use link that works for 7 days. Send it to them yourself.</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="grid gap-3 sm:grid-cols-[2fr_1fr_auto] sm:items-start" novalidate @submit.prevent="submit">
          <FormField v-slot="{ componentField }" name="email">
            <FormItem>
              <FormLabel>Email (optional)</FormLabel>
              <FormControl><Input type="email" placeholder="colleague@example.com" v-bind="componentField" /></FormControl>
              <FormDescription>Leave empty to let them fill in their own.</FormDescription>
              <FormMessage />
            </FormItem>
          </FormField>
          <FormField v-slot="{ componentField }" name="role">
            <FormItem>
              <FormLabel>Role</FormLabel>
              <Select v-bind="componentField">
                <FormControl><SelectTrigger class="w-full"><SelectValue /></SelectTrigger></FormControl>
                <SelectContent>
                  <SelectItem value="user">Member</SelectItem>
                  <SelectItem value="admin">Admin</SelectItem>
                </SelectContent>
              </Select>
            </FormItem>
          </FormField>
          <Button type="submit" class="sm:mt-[1.375rem]" :disabled="create.isPending.value"><Plus /> Create invite</Button>
        </form>
        <div v-if="fresh?.url" class="mt-4 grid gap-2 rounded-lg border bg-muted/40 p-3">
          <p class="text-sm font-medium">Invite link (shown only now)</p>
          <div class="flex items-center gap-1">
            <code class="min-w-0 flex-1 font-mono text-xs break-all">{{ fresh.url }}</code>
            <CopyButton :value="fresh.url" what="Invite link copied" label="Copy" variant="secondary" />
          </div>
        </div>
      </CardContent>
    </Card>

    <QueryError v-if="invites.isError.value" :error="invites.error.value" @retry="invites.refetch()" />
    <Skeleton v-else-if="invites.isPending.value" class="h-24 w-full" />
    <EmptyState v-else-if="!invites.data.value?.length" title="No invites yet." description="Create one above to bring a colleague in." />
    <div v-else class="overflow-x-auto rounded-xl border bg-card shadow-card">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>For</TableHead>
            <TableHead>Status</TableHead>
            <TableHead class="hidden sm:table-cell">Created</TableHead>
            <TableHead class="w-0" />
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="i in invites.data.value" :key="i.id">
            <TableCell>
              {{ i.email || 'Anyone with the link' }}
              <Badge v-if="i.role === 'admin'" variant="secondary" class="ml-1">Admin</Badge>
            </TableCell>
            <TableCell>
              <span :class="state(i).tone">{{ state(i).label }}</span><span class="text-muted-foreground">{{ state(i).label === 'Waiting' ? ', expires' : '' }} <RelativeTime :date="state(i).date" /><template v-if="i.used_at && usedBy(i)"> by {{ usedBy(i) }}</template></span>
            </TableCell>
            <TableCell class="hidden sm:table-cell"><RelativeTime :date="i.created_at" /></TableCell>
            <TableCell>
              <Button v-if="!i.used_at" size="sm" variant="ghost" class="text-destructive" :aria-label="`Revoke invite for ${i.email || 'anyone with the link'}`" @click="toRevoke = i">Revoke</Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <div class="mt-4 grid gap-1">
      <h2 class="text-base font-semibold">Project invites</h2>
      <p class="text-sm text-muted-foreground">Links people made to share their projects, not used yet.</p>
    </div>
    <QueryError v-if="projectInvites.isError.value" :error="projectInvites.error.value" @retry="projectInvites.refetch()" />
    <Skeleton v-else-if="projectInvites.isPending.value" class="h-24 w-full" />
    <EmptyState v-else-if="!projectInvites.data.value?.length" title="No open project invites." description="Invites that project owners make in their Members tab show up here." />
    <div v-else class="overflow-x-auto rounded-xl border bg-card shadow-card">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Project</TableHead>
            <TableHead>Invited by</TableHead>
            <TableHead class="hidden sm:table-cell">For</TableHead>
            <TableHead class="hidden md:table-cell">Created</TableHead>
            <TableHead class="hidden sm:table-cell">Expires</TableHead>
            <TableHead class="w-0" />
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="p in projectInvites.data.value" :key="p.id">
            <TableCell class="font-medium">
              {{ p.project.name }}
              <span class="block text-xs font-normal text-muted-foreground sm:hidden">{{ p.email || 'Anyone with the link' }}</span>
            </TableCell>
            <TableCell>
              <template v-if="p.invited_by">{{ p.invited_by.name || p.invited_by.email }}<span class="block text-xs text-muted-foreground">{{ p.invited_by.email }}</span></template>
              <span v-else class="text-muted-foreground">A deleted account</span>
            </TableCell>
            <TableCell class="hidden sm:table-cell">{{ p.email || 'Anyone with the link' }}</TableCell>
            <TableCell class="hidden md:table-cell"><RelativeTime :date="p.created_at" /></TableCell>
            <TableCell class="hidden text-muted-foreground sm:table-cell"><RelativeTime :date="p.expires_at" /></TableCell>
            <TableCell>
              <Button size="sm" variant="ghost" class="text-destructive" :aria-label="`Revoke the invite to ${p.project.name}`" @click="toRevokeProject = p">Revoke</Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <ConfirmDialog
      v-model:open="revokeProjectOpen"
      title="Revoke this project invite?"
      :description="`The link to ${toRevokeProject?.project.name ?? 'the project'} stops working straight away. The person who made it is not told.`"
      confirm-label="Revoke invite"
      :loading="revokeProject.isPending.value"
      @confirm="toRevokeProject && revokeProject.mutate(toRevokeProject.id)"
    />

    <ConfirmDialog
      v-model:open="revokeOpen"
      title="Revoke this invite?"
      description="The link stops working straight away."
      confirm-label="Revoke invite"
      :loading="revoke.isPending.value"
      @confirm="toRevoke && revoke.mutate(toRevoke.id)"
    />
  </div>
</template>
