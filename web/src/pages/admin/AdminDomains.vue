<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { ChevronRight, Plus } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyButton from '@/components/CopyButton.vue'
import QueryError from '@/components/QueryError.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import type { AppDomain } from '@/lib/types'

const qc = useQueryClient()
const list = useQuery({ queryKey: keys.adminAppDomains, queryFn: api.adminAppDomains, refetchInterval: 30_000 })
const ipv4 = computed(() => list.data.value?.server_ipv4 ?? '')
const ipv6 = computed(() => list.data.value?.server_ipv6 ?? '')

function refresh() {
  void qc.invalidateQueries({ queryKey: keys.adminAppDomains })
  void qc.invalidateQueries({ queryKey: keys.adminUsers })
  void qc.invalidateQueries({ queryKey: keys.me }) // admins may use every domain
}

// ---------------------------------------------------------------- add
const domain = ref('')
const cert = ref('')
const key = ref('')
const showCert = ref(false)
const example = computed(() => domain.value.trim().toLowerCase().replace(/^\*\./, '') || 'example.com')

const add = useMutation({
  mutationFn: () =>
    api.addAppDomain({ domain: domain.value.trim(), certificate: cert.value.trim() || undefined, private_key: key.value.trim() || undefined }),
  onSuccess: (d) => {
    toastOk(`${d.domain} added`, 'Choose who may use it under People.')
    domain.value = cert.value = key.value = ''
    showCert.value = false
    refresh()
  },
  onError: (e) => toastError(e),
})

// ---------------------------------------------------------------- certificate
const certFor = ref<AppDomain | null>(null)
const certOpen = computed({
  get: () => certFor.value !== null,
  set: (v) => {
    if (!v) certFor.value = null
  },
})
const newCert = ref('')
const newKey = ref('')
function openCert(d: AppDomain) {
  newCert.value = newKey.value = ''
  certFor.value = d
}
const saveCert = useMutation({
  mutationFn: (d: AppDomain) => api.setAppDomainCert(d.id, { certificate: newCert.value.trim(), private_key: newKey.value.trim() }),
  onSuccess: (d) => {
    toastOk(`Certificate saved for ${d.domain}`)
    certFor.value = null
    refresh()
  },
  onError: (e) => toastError(e),
})

// ---------------------------------------------------------------- remove
const toRemove = ref<AppDomain | null>(null)
const removeOpen = computed({
  get: () => toRemove.value !== null,
  set: (v) => {
    if (!v) toRemove.value = null
  },
})
const remove = useMutation({
  mutationFn: (d: AppDomain) => api.removeAppDomain(d.id),
  onSuccess: (_r, d) => {
    toastOk(`${d.domain} removed`)
    toRemove.value = null
    refresh()
  },
  onError: (e) => {
    toRemove.value = null
    toastError(e)
  },
})

function who(d: AppDomain): string {
  if (d.default) return 'Everyone'
  if (d.user_count === 0) return 'Only admins'
  return d.user_count === 1 ? '1 person and admins' : `${d.user_count} people and admins`
}
function certInfo(d: AppDomain): { label: string; tone: string } {
  if (d.certificate === 'install') return { label: 'Set up with bitploy', tone: 'text-muted-foreground' }
  if (d.certificate === 'none' || !d.cert_expires_at) return { label: 'None, fine for Full', tone: 'text-muted-foreground' }
  const until = new Date(d.cert_expires_at)
  const soon = until.getTime() - Date.now() < 30 * 86_400_000
  return { label: `Valid until ${until.toLocaleDateString()}`, tone: soon ? 'text-destructive' : 'text-muted-foreground' }
}
</script>

<template>
  <div class="grid gap-4">
    <QueryError v-if="list.isError.value" :error="list.error.value" @retry="list.refetch()" />
    <Skeleton v-else-if="list.isPending.value" class="h-24 w-full" />
    <div v-else class="overflow-x-auto rounded-xl border bg-card shadow-card">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Domain</TableHead>
            <TableHead>Apps</TableHead>
            <TableHead class="hidden sm:table-cell">Who can use it</TableHead>
            <TableHead class="hidden md:table-cell">Certificate</TableHead>
            <TableHead class="w-0" />
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="d in list.data.value?.items" :key="d.id">
            <TableCell>
              <span class="font-mono text-[13px]">{{ d.domain }}</span>
              <Badge v-if="d.default" variant="secondary" class="ml-1">Default</Badge>
            </TableCell>
            <TableCell class="tabular-nums">{{ d.app_count }}</TableCell>
            <TableCell class="hidden text-sm sm:table-cell">{{ who(d) }}</TableCell>
            <TableCell class="hidden text-sm md:table-cell" :class="certInfo(d).tone">{{ certInfo(d).label }}</TableCell>
            <TableCell>
              <div v-if="!d.default" class="flex gap-1">
                <Button size="sm" variant="outline" :aria-label="`Certificate for ${d.domain}`" @click="openCert(d)">
                  {{ d.certificate === 'none' ? 'Add certificate' : 'Replace certificate' }}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  class="text-destructive"
                  :disabled="d.app_count > 0"
                  :title="d.app_count > 0 ? 'Move or delete the apps on this domain first.' : undefined"
                  :aria-label="`Remove ${d.domain}`"
                  @click="toRemove = d"
                >
                  Remove
                </Button>
              </div>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <Card>
      <CardHeader>
        <CardTitle>Add a domain</CardTitle>
        <CardDescription>Apps on it get an address like <span class="font-mono">name.{{ example }}</span>. The domain has to be on Cloudflare.</CardDescription>
      </CardHeader>
      <CardContent class="grid gap-5">
        <ol class="grid list-decimal gap-3 pl-5 text-sm">
          <li>
            In Cloudflare, open {{ example }} and go to <b>DNS</b>. Add a record of type <b>A</b>, name <span class="font-mono">*</span> (the star means every name under the domain), with the orange cloud on
            (<b>Proxied</b>), pointing at this server:
            <span v-if="ipv4" class="mt-1.5 flex items-center gap-1">
              <code class="rounded bg-muted px-1.5 py-0.5 font-mono text-[13px]">{{ ipv4 }}</code>
              <CopyButton :value="ipv4" what="IP address copied" />
            </span>
            <span v-else class="mt-1 block text-muted-foreground">
              Bitploy does not know this server's IP address. Use the one your hosting provider shows, or set BITPLOY_SERVER_IPV4 in the server settings.
            </span>
            <span v-if="ipv6" class="mt-2 block">
              The server also has an IPv6 address, so add a record of type <b>AAAA</b>, name <span class="font-mono">*</span>, also Proxied:
              <span class="mt-1.5 flex items-center gap-1">
                <code class="rounded bg-muted px-1.5 py-0.5 font-mono text-[13px] break-all">{{ ipv6 }}</code>
                <CopyButton :value="ipv6" what="IP address copied" />
              </span>
            </span>
          </li>
          <li>
            Go to <b>SSL/TLS → Overview</b> and set the encryption mode to <b>Full</b>. Cloudflare then encrypts the connection to this server without checking its
            certificate, so you don't need one.
          </li>
          <li>Add the domain below. Then choose who may use it under <b>People</b>. Admins can always use every domain.</li>
        </ol>

        <form class="grid gap-4" novalidate @submit.prevent="add.mutate()">
          <div class="grid gap-1.5 sm:max-w-sm">
            <Label for="app-domain">Domain</Label>
            <Input id="app-domain" v-model="domain" placeholder="example.com" autocapitalize="off" autocomplete="off" spellcheck="false" class="font-mono" />
          </div>
          <Collapsible v-model:open="showCert">
            <CollapsibleTrigger as-child>
              <button type="button" class="inline-flex items-center gap-1 text-sm font-medium hover:underline">
                <ChevronRight :class="['size-4 transition-transform', showCert && 'rotate-90']" /> Optional: a certificate for Full (strict)
              </button>
            </CollapsibleTrigger>
            <CollapsibleContent class="mt-3 grid gap-4">
              <p class="text-xs text-muted-foreground">
                In <b>Full (strict)</b> mode Cloudflare also checks that this server has a valid certificate for the domain. To use it, go to
                <b>SSL/TLS → Origin Server</b>, click <b>Create certificate</b> for <span class="font-mono">{{ example }}</span> and
                <span class="font-mono">*.{{ example }}</span>, paste both parts here, and switch the mode to Full (strict) once the domain is added. You can also do this
                later. The private key is stored encrypted and never shown again.
              </p>
              <div class="grid gap-4 lg:grid-cols-2">
                <div class="grid gap-1.5">
                  <Label for="app-domain-cert">Origin certificate</Label>
                  <Textarea id="app-domain-cert" v-model="cert" rows="5" placeholder="-----BEGIN CERTIFICATE-----" spellcheck="false" class="font-mono text-xs" />
                </div>
                <div class="grid gap-1.5">
                  <Label for="app-domain-key">Private key</Label>
                  <Textarea id="app-domain-key" v-model="key" rows="5" placeholder="-----BEGIN PRIVATE KEY-----" autocomplete="off" spellcheck="false" class="font-mono text-xs" />
                </div>
              </div>
            </CollapsibleContent>
          </Collapsible>
          <div><Button type="submit" :disabled="add.isPending.value || !domain.trim()"><Plus /> {{ add.isPending.value ? 'Adding...' : 'Add domain' }}</Button></div>
        </form>
      </CardContent>
    </Card>

    <Dialog v-model:open="certOpen">
      <DialogContent class="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Certificate for {{ certFor?.domain }}</DialogTitle>
          <DialogDescription>
            Only needed for Cloudflare's Full (strict) mode. In Cloudflare, go to SSL/TLS → Origin Server → Create certificate for {{ certFor?.domain }} and
            *.{{ certFor?.domain }}, paste both parts here, then set the mode to Full (strict).
          </DialogDescription>
        </DialogHeader>
        <form id="app-domain-cert-form" class="grid gap-4" novalidate @submit.prevent="certFor && saveCert.mutate(certFor)">
          <div class="grid gap-1.5">
            <Label for="new-cert">Origin certificate</Label>
            <Textarea id="new-cert" v-model="newCert" rows="5" placeholder="-----BEGIN CERTIFICATE-----" spellcheck="false" class="font-mono text-xs" />
          </div>
          <div class="grid gap-1.5">
            <Label for="new-key">Private key</Label>
            <Textarea id="new-key" v-model="newKey" rows="5" placeholder="-----BEGIN PRIVATE KEY-----" autocomplete="off" spellcheck="false" class="font-mono text-xs" />
          </div>
        </form>
        <DialogFooter>
          <Button variant="outline" @click="certFor = null">Cancel</Button>
          <Button type="submit" form="app-domain-cert-form" :disabled="saveCert.isPending.value || !newCert.trim() || !newKey.trim()">
            {{ saveCert.isPending.value ? 'Saving...' : 'Save certificate' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      v-model:open="removeOpen"
      :title="`Remove ${toRemove?.domain}?`"
      description="Nobody can put new apps on it any more, and its certificate is deleted from the server. People who could use it lose access."
      confirm-label="Remove"
      :loading="remove.isPending.value"
      @confirm="toRemove && remove.mutate(toRemove)"
    />
  </div>
</template>
