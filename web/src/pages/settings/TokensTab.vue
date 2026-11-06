<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { KeyRound, Plus, Trash2 } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyButton from '@/components/CopyButton.vue'
import EmptyState from '@/components/EmptyState.vue'
import McpSnippets from '@/components/McpSnippets.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import SettingsSection from '@/components/SettingsSection.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { keys, useMe } from '@/composables/queries'
import { api } from '@/lib/api'
import { rememberToken } from '@/lib/mcp'
import { toastError, toastOk } from '@/lib/notify'
import type { Token, TokenCreated } from '@/lib/types'

const qc = useQueryClient()
const me = useMe()
const tokens = useQuery({ queryKey: keys.tokens, queryFn: api.tokens })
const mcpUrl = computed(() => `${me.data.value?.public_url ?? window.location.origin}/mcp`)

const createOpen = ref(false)
const created = ref<TokenCreated | null>(null)
const createdOpen = computed({
  get: () => created.value !== null,
  set: (v) => {
    if (!v) created.value = null
  },
})

const { handleSubmit, resetForm } = useForm({
  validationSchema: toTypedSchema(
    z.object({
      name: z.string().trim().min(1, 'Give the token a name, like "My laptop".').max(60, 'Keep the name under 60 characters.'),
      scope: z.enum(['deploy', 'read']),
      expires: z.enum(['0', '30', '90']),
    }),
  ),
  initialValues: { name: '', scope: 'deploy' as const, expires: '0' as const },
})

const create = useMutation({
  mutationFn: (v: { name: string; scope: 'read' | 'deploy'; expires: string }) =>
    api.createToken({ name: v.name.trim(), scope: v.scope, expires_in_days: Number(v.expires) || undefined }),
  onSuccess: (t) => {
    rememberToken(t.value)
    createOpen.value = false
    created.value = t
    resetForm()
    void qc.invalidateQueries({ queryKey: keys.tokens })
  },
  onError: (e) => toastError(e),
})
const submit = handleSubmit((v) => create.mutate(v))

const toRevoke = ref<Token | null>(null)
const revokeOpen = computed({
  get: () => toRevoke.value !== null,
  set: (v) => {
    if (!v) toRevoke.value = null
  },
})
const revoke = useMutation({
  mutationFn: (id: string) => api.revokeToken(id),
  onSuccess: () => {
    toastOk('Token revoked')
    toRevoke.value = null
    void qc.invalidateQueries({ queryKey: keys.tokens })
  },
  onError: (e) => toastError(e),
})

function expiryState(t: Token): 'never' | 'expired' | 'date' {
  if (!t.expires_at) return 'never'
  return new Date(t.expires_at).getTime() < Date.now() ? 'expired' : 'date'
}
</script>

<template>
  <SettingsSection title="API tokens" description="A token is a password for your AI assistant: it lets the assistant put apps online for you. Use one token per assistant.">
    <template v-if="tokens.data.value?.length" #aside>
      <Button size="sm" @click="createOpen = true"><Plus /> New token</Button>
    </template>

    <QueryError v-if="tokens.isError.value" :error="tokens.error.value" @retry="tokens.refetch()" />
    <div v-else-if="tokens.isPending.value" class="grid gap-2"><Skeleton v-for="i in 2" :key="i" class="h-12 w-full" /></div>
    <EmptyState
      v-else-if="!tokens.data.value?.length"
      :icon="KeyRound"
      title="No tokens yet."
      description="Create one, then add it to your AI assistant. The Connect AI page shows you how."
    >
      <Button @click="createOpen = true"><Plus /> New token</Button>
      <Button as-child variant="outline"><RouterLink to="/connect">Open Connect AI</RouterLink></Button>
    </EmptyState>
    <div v-else class="overflow-hidden rounded-xl border bg-card shadow-card">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Can</TableHead>
            <TableHead class="hidden 2xl:table-cell">Created</TableHead>
            <TableHead class="hidden sm:table-cell">Last used</TableHead>
            <TableHead class="hidden md:table-cell">Expires</TableHead>
            <TableHead class="w-0" />
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="t in tokens.data.value" :key="t.id">
            <TableCell>
              <p class="font-medium">{{ t.name }}</p>
              <p class="font-mono text-xs text-muted-foreground">{{ t.prefix }}...</p>
            </TableCell>
            <TableCell>
              <Badge :variant="t.scope === 'deploy' ? 'outline' : 'secondary'">{{ t.scope === 'deploy' ? 'Deploy' : 'Read only' }}</Badge>
            </TableCell>
            <TableCell class="hidden 2xl:table-cell"><RelativeTime :date="t.created_at" /></TableCell>
            <TableCell class="hidden sm:table-cell"><RelativeTime :date="t.last_used_at" empty="Never used" /></TableCell>
            <TableCell class="hidden md:table-cell">
              <span v-if="t.revoked_at" class="text-muted-foreground">Revoked</span>
              <span v-else-if="expiryState(t) === 'never'" class="text-muted-foreground">Never</span>
              <span v-else-if="expiryState(t) === 'expired'" class="text-destructive">Expired</span>
              <RelativeTime v-else :date="t.expires_at" />
            </TableCell>
            <TableCell>
              <Button v-if="!t.revoked_at" size="sm" variant="ghost" class="text-destructive" :aria-label="`Revoke token ${t.name}`" @click="toRevoke = t"><Trash2 /> Revoke</Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <Dialog v-model:open="createOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New token</DialogTitle>
          <DialogDescription>You will see the token once, right after you create it.</DialogDescription>
        </DialogHeader>
        <form id="create-token" class="grid gap-4" novalidate @submit.prevent="submit">
          <FormField v-slot="{ componentField }" name="name">
            <FormItem>
              <FormLabel>Name</FormLabel>
              <FormControl><Input placeholder="My laptop" autocomplete="off" v-bind="componentField" /></FormControl>
              <FormMessage />
            </FormItem>
          </FormField>
          <FormField v-slot="{ componentField }" name="scope">
            <FormItem>
              <FormLabel>What can it do?</FormLabel>
              <Select v-bind="componentField">
                <FormControl><SelectTrigger class="w-full"><SelectValue /></SelectTrigger></FormControl>
                <SelectContent>
                  <SelectItem value="deploy">Deploy: put apps online and manage them</SelectItem>
                  <SelectItem value="read">Read only: look at apps, logs and usage</SelectItem>
                </SelectContent>
              </Select>
              <FormMessage />
            </FormItem>
          </FormField>
          <FormField v-slot="{ componentField }" name="expires">
            <FormItem>
              <FormLabel>Stops working</FormLabel>
              <Select v-bind="componentField">
                <FormControl><SelectTrigger class="w-full"><SelectValue /></SelectTrigger></FormControl>
                <SelectContent>
                  <SelectItem value="0">Never</SelectItem>
                  <SelectItem value="30">After 30 days</SelectItem>
                  <SelectItem value="90">After 90 days</SelectItem>
                </SelectContent>
              </Select>
              <FormMessage />
            </FormItem>
          </FormField>
        </form>
        <DialogFooter>
          <Button variant="outline" @click="createOpen = false">Cancel</Button>
          <Button type="submit" form="create-token" :disabled="create.isPending.value">{{ create.isPending.value ? 'Creating...' : 'Create token' }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="createdOpen">
      <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Your new token</DialogTitle>
          <DialogDescription>Copy it now. It is not shown again.</DialogDescription>
        </DialogHeader>
        <template v-if="created">
          <div class="flex items-center gap-1 rounded-lg border bg-muted/40 py-1 pr-1 pl-3">
            <code class="min-w-0 flex-1 font-mono text-sm break-all">{{ created.value }}</code>
            <CopyButton :value="created.value" what="Token copied" label="Copy" variant="secondary" />
          </div>
          <Alert>
            <AlertTitle>Add it to your AI assistant</AlertTitle>
            <AlertDescription>The snippets below already contain your token. Pick your tool and copy.</AlertDescription>
          </Alert>
          <McpSnippets :mcp-url="mcpUrl" :token="created.value" />
        </template>
        <DialogFooter>
          <Button as-child variant="outline"><RouterLink to="/connect" @click="created = null">Open Connect AI</RouterLink></Button>
          <Button @click="created = null">Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      v-model:open="revokeOpen"
      title="Revoke this token?"
      :description="`${toRevoke?.name ?? 'This token'} stops working straight away. Any assistant using it will need a new token.`"
      confirm-label="Revoke token"
      :loading="revoke.isPending.value"
      @confirm="toRevoke && revoke.mutate(toRevoke.id)"
    />
  </SettingsSection>
</template>
