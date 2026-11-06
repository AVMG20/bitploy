<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { keys, REFRESH_MS } from '@/composables/queries'
import { api } from '@/lib/api'
import { appPath } from '@/lib/appLinks'
import { toastError, toastOk } from '@/lib/notify'

// The public port range of the server (BITPLOY_PUBLIC_PORTS) and the ports
// open now, each with a button to close it.
const qc = useQueryClient()
const ports = useQuery({ queryKey: keys.adminPublicPorts, queryFn: api.adminPublicPorts, refetchInterval: REFRESH_MS })
const p = computed(() => ports.data.value)
const size = computed(() => (p.value?.enabled ? p.value.to - p.value.from + 1 : 0))

const close = useMutation({
  mutationFn: (slug: string) => api.adminClosePublicPort(slug),
  onSuccess: (_r, slug) => {
    void qc.invalidateQueries({ queryKey: keys.adminPublicPorts })
    void qc.invalidateQueries({ queryKey: keys.app(slug) })
    toastOk('Public port closed')
  },
  onError: (e) => toastError(e),
})
</script>

<template>
  <Card data-testid="admin-public-ports">
    <CardHeader>
      <CardTitle>Public ports</CardTitle>
      <CardDescription>Ports on the server that forward to a database or worker, opened by its owner to connect from outside.</CardDescription>
    </CardHeader>
    <CardContent class="grid gap-4">
      <QueryError v-if="ports.isError.value" :error="ports.error.value" @retry="ports.refetch()" />
      <Skeleton v-else-if="!p" class="h-16 w-full" />
      <div v-else-if="!p.enabled" class="grid gap-1 text-sm text-muted-foreground">
        <p>Off. Nobody can open a public port.</p>
        <p>
          To turn them on, set <span class="font-mono text-foreground/85">BITPLOY_PUBLIC_PORTS=30000-30099</span> in
          <span class="font-mono text-foreground/85">/etc/bitploy/bitploy.env</span>, open the same ports in the server's firewall
          (Hetzner Cloud Firewall), then run <span class="font-mono text-foreground/85">systemctl restart bitploy-firewall</span> and restart bitploy.
        </p>
      </div>
      <template v-else>
        <dl class="grid gap-3 text-sm sm:grid-cols-3">
          <div>
            <dt class="text-xs text-muted-foreground">Range</dt>
            <dd class="font-mono">{{ p.from }}-{{ p.to }}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted-foreground">In use</dt>
            <dd class="tabular-nums">{{ p.open.length }} of {{ size }}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted-foreground">Per person</dt>
            <dd class="tabular-nums">at most {{ p.per_user }}</dd>
          </div>
        </dl>
        <p class="text-xs text-muted-foreground">Addresses use <span class="font-mono text-foreground/85">{{ p.host }}</span>.</p>

        <p v-if="!p.open.length" class="rounded-lg border border-dashed px-3 py-3 text-sm text-muted-foreground">No port is open.</p>
        <div v-else class="overflow-x-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow class="hover:bg-transparent">
                <TableHead>App</TableHead>
                <TableHead class="hidden sm:table-cell">Owner</TableHead>
                <TableHead>Port</TableHead>
                <TableHead class="hidden md:table-cell">Who can connect</TableHead>
                <TableHead>Closes</TableHead>
                <th class="w-0"><span class="sr-only">Actions</span></th>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="o in p.open" :key="o.app">
                <TableCell>
                  <RouterLink :to="appPath(o.app)" class="font-medium hover:underline">{{ o.app_name }}</RouterLink>
                  <p v-if="o.app !== o.app_name" class="font-mono text-xs text-muted-foreground">{{ o.app }}</p>
                  <p v-if="o.service" class="font-mono text-xs text-muted-foreground">{{ o.service }}</p>
                  <p v-if="o.error" class="text-xs text-destructive">{{ o.error }}</p>
                </TableCell>
                <TableCell class="hidden sm:table-cell">{{ o.owner_email }}</TableCell>
                <TableCell class="font-mono tabular-nums">{{ o.port }}</TableCell>
                <TableCell class="hidden text-xs md:table-cell"><span v-if="o.allow_from.length" class="font-mono">{{ o.allow_from.join(', ') }}</span><template v-else>Anyone</template></TableCell>
                <TableCell class="text-xs"><RelativeTime :date="o.expires_at" empty="never" /></TableCell>
                <TableCell>
                  <Button size="sm" variant="outline" :aria-label="`Close the public port of ${o.app_name}`" :disabled="close.isPending.value" @click="close.mutate(o.app)">Close port</Button>
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>
      </template>
    </CardContent>
  </Card>
</template>
