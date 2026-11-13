<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Link2 } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyButton from '@/components/CopyButton.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import SettingsSection from '@/components/SettingsSection.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { keys, useSignupRules } from '@/composables/queries'
import { useProjectData } from '@/composables/useProjectData'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import { quietDestructive } from '@/lib/utils'
import { ownerName } from '@/lib/projects'
import { projectInviteCardText } from '@/lib/signups'
import type { ProjectInvite, ProjectMember } from '@/lib/types'

// Who can use the project: its owner and the people it is shared with. The
// owner invites with single-use links (like an admin invites people to
// bitploy), sees the open invites and removes members; a member can leave.
const props = defineProps<{ projectId: string }>()
const router = useRouter()
const qc = useQueryClient()
const { project } = useProjectData(() => props.projectId)
const isOwner = computed(() => project.value?.role === 'owner')
// Whether a project invite can also create an account follows Admin > Signups.
const rules = useSignupRules()
const inviteText = computed(() => projectInviteCardText(rules.data.value?.project_invite_signup ?? true))

const members = useQuery({
  queryKey: computed(() => keys.projectMembers(props.projectId)),
  queryFn: () => api.projectMembers(props.projectId),
})
const invites = useQuery({
  queryKey: computed(() => keys.projectInvites(props.projectId)),
  queryFn: () => api.projectInvites(props.projectId),
  enabled: isOwner,
  refetchInterval: 15_000,
})
const refresh = () => {
  void qc.invalidateQueries({ queryKey: keys.projectMembers(props.projectId) })
  void qc.invalidateQueries({ queryKey: keys.projectInvites(props.projectId) })
}

const fresh = ref<ProjectInvite | null>(null)
const { handleSubmit, resetForm } = useForm({
  validationSchema: toTypedSchema(z.object({ email: z.string().trim().email('That does not look like an email address.').or(z.literal('')) })),
  initialValues: { email: '' },
})
const create = useMutation({
  mutationFn: (v: { email: string }) => api.createProjectInvite(props.projectId, { email: v.email || undefined }),
  onSuccess: (inv) => {
    fresh.value = inv
    resetForm()
    toastOk('Invite link made', 'Send it to them yourself. It works once, for 7 days.')
    refresh()
  },
  onError: (e) => toastError(e),
})
const submit = handleSubmit((v) => create.mutate(v))

const toRevoke = ref<ProjectInvite | null>(null)
const revokeOpen = computed({
  get: () => toRevoke.value !== null,
  set: (v) => {
    if (!v) toRevoke.value = null
  },
})
const revoke = useMutation({
  mutationFn: (id: string) => api.revokeProjectInvite(props.projectId, id),
  onSuccess: () => {
    toastOk('Invite revoked')
    if (fresh.value?.id === toRevoke.value?.id) fresh.value = null
    toRevoke.value = null
    refresh()
  },
  onError: (e) => toastError(e),
})

const toRemove = ref<ProjectMember | null>(null)
const removeOpen = computed({
  get: () => toRemove.value !== null,
  set: (v) => {
    if (!v) toRemove.value = null
  },
})
const leaving = computed(() => !!toRemove.value?.you)
const remove = useMutation({
  mutationFn: (m: ProjectMember) => api.removeProjectMember(props.projectId, m.you ? 'me' : m.user_id),
  onSuccess: () => {
    if (leaving.value) {
      toastOk(`You left ${project.value?.name ?? 'the project'}`)
      toRemove.value = null
      void qc.invalidateQueries({ queryKey: keys.apps })
      void router.push('/')
      return
    }
    toastOk(`Removed ${toRemove.value?.name || toRemove.value?.email || 'the member'}`)
    toRemove.value = null
    refresh()
  },
  onError: (e) => toastError(e),
})
const removeText = computed(() => {
  const m = toRemove.value
  if (!m) return ''
  if (m.you) return `You lose access to ${project.value?.name ?? 'the project'} and its apps straight away. The apps stay as they are. To come back you need a new invite from ${project.value ? ownerName(project.value) : 'the owner'}.`
  return `${m.name || m.email} loses access to this project and its apps straight away, also with their API tokens. The apps stay as they are.`
})
</script>

<template>
  <div v-if="project" class="grid max-w-6xl gap-10">
    <SettingsSection
      v-if="isOwner"
      title="Invite someone"
      description="Members can deploy the project's apps and change their settings, variables and data. Only you can invite and remove people, delete apps, files and backups, restore backups, and delete the project."
    >
      <Card>
        <CardHeader>
          <CardTitle>Invite link</CardTitle>
          <CardDescription>{{ inviteText }}</CardDescription>
        </CardHeader>
        <CardContent>
          <form class="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start" novalidate @submit.prevent="submit">
            <FormField v-slot="{ componentField }" name="email">
              <FormItem>
                <FormLabel>Email (optional)</FormLabel>
                <FormControl><Input type="email" placeholder="colleague@example.com" autocomplete="off" v-bind="componentField" /></FormControl>
                <FormDescription>Only the account with this address can use the link. Leave empty for anyone who has it.</FormDescription>
                <FormMessage />
              </FormItem>
            </FormField>
            <Button type="submit" class="sm:mt-[1.375rem]" :disabled="create.isPending.value"><Link2 /> {{ create.isPending.value ? 'Making...' : 'Make invite link' }}</Button>
          </form>
          <div v-if="fresh?.url" class="mt-4 grid gap-2 rounded-lg border bg-muted/40 p-3">
            <p class="text-sm font-medium">Invite link{{ fresh.email ? ` for ${fresh.email}` : '' }} (shown only now)</p>
            <div class="flex items-center gap-1">
              <code class="min-w-0 flex-1 font-mono text-xs break-all">{{ fresh.url }}</code>
              <CopyButton :value="fresh.url" what="Invite link copied" label="Copy" variant="secondary" />
            </div>
          </div>
        </CardContent>
      </Card>
    </SettingsSection>

    <SettingsSection
      title="Members"
      :description="isOwner ? 'Apps members add belong to you and count towards your limits.' : `${ownerName(project)} owns this project. Apps you add here belong to them and count towards their limits.`"
    >
      <QueryError v-if="members.isError.value" :error="members.error.value" @retry="members.refetch()" />
      <Skeleton v-else-if="members.isPending.value" class="h-24 w-full" />
      <div v-else class="overflow-x-auto rounded-xl border bg-card shadow-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Role</TableHead>
              <TableHead class="hidden sm:table-cell">Joined</TableHead>
              <TableHead class="w-0" />
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="m in members.data.value" :key="m.user_id">
              <TableCell class="max-w-0 min-w-40">
                <p class="truncate font-medium">{{ m.name || m.email }}<span v-if="m.you" class="font-normal text-muted-foreground"> (you)</span></p>
                <p class="truncate text-xs text-muted-foreground">{{ m.email }}</p>
              </TableCell>
              <TableCell>
                <Badge :variant="m.role === 'owner' ? 'secondary' : 'outline'">{{ m.role === 'owner' ? 'Owner' : 'Member' }}</Badge>
              </TableCell>
              <TableCell class="hidden text-muted-foreground sm:table-cell"><RelativeTime :date="m.created_at" /></TableCell>
              <TableCell class="text-right">
                <Button v-if="m.role === 'member' && m.you" size="sm" variant="ghost" :class="quietDestructive" @click="toRemove = m">Leave</Button>
                <Button v-else-if="m.role === 'member' && isOwner" size="sm" variant="ghost" :class="quietDestructive" :aria-label="`Remove ${m.name || m.email}`" @click="toRemove = m">Remove</Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
      <p v-if="isOwner && members.data.value?.length === 1" class="mt-3 text-sm text-muted-foreground">Only you so far. Make an invite link above to share this project.</p>
    </SettingsSection>

    <SettingsSection v-if="isOwner" title="Open invites">
      <QueryError v-if="invites.isError.value" :error="invites.error.value" @retry="invites.refetch()" />
      <Skeleton v-else-if="invites.isPending.value" class="h-16 w-full" />
      <p v-else-if="!invites.data.value?.length" class="rounded-xl border border-dashed p-4 text-sm text-muted-foreground">No open invites.</p>
      <div v-else class="overflow-x-auto rounded-xl border bg-card shadow-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>For</TableHead>
              <TableHead>Expires</TableHead>
              <TableHead class="hidden sm:table-cell">Made</TableHead>
              <TableHead class="w-0" />
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="i in invites.data.value" :key="i.id">
              <TableCell class="max-w-0 min-w-40"><span class="block truncate">{{ i.email || 'Anyone with the link' }}</span></TableCell>
              <TableCell class="text-muted-foreground"><RelativeTime :date="i.expires_at" /></TableCell>
              <TableCell class="hidden text-muted-foreground sm:table-cell"><RelativeTime :date="i.created_at" /></TableCell>
              <TableCell class="text-right">
                <Button size="sm" variant="ghost" :class="quietDestructive" :aria-label="`Revoke invite for ${i.email || 'anyone with the link'}`" @click="toRevoke = i">Revoke</Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </SettingsSection>

    <ConfirmDialog
      v-model:open="revokeOpen"
      title="Revoke this invite?"
      description="The link stops working straight away."
      confirm-label="Revoke invite"
      :loading="revoke.isPending.value"
      @confirm="toRevoke && revoke.mutate(toRevoke.id)"
    />
    <ConfirmDialog
      v-model:open="removeOpen"
      :title="leaving ? `Leave ${project.name}?` : `Remove ${toRemove?.name || toRemove?.email}?`"
      :description="removeText"
      :confirm-label="leaving ? 'Leave project' : 'Remove member'"
      :loading="remove.isPending.value"
      @confirm="toRemove && remove.mutate(toRemove)"
    />
  </div>
</template>
