<script setup lang="ts">
import { computed, ref } from "vue";
import { RouterLink } from "vue-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { Square } from "lucide-vue-next";
import AppStatusBadge from "@/components/AppStatusBadge.vue";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import EmptyState from "@/components/EmptyState.vue";
import StatCard from "@/components/StatCard.vue";
import QueryError from "@/components/QueryError.vue";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { keys, REFRESH_MS } from "@/composables/queries";
import { api } from "@/lib/api";
import { formatMB, formatPercent, hostOf, pct } from "@/lib/format";
import { toastError, toastOk } from "@/lib/notify";
import type { App } from "@/lib/types";

const qc = useQueryClient();
const apps = useQuery({
  queryKey: keys.adminApps,
  queryFn: api.adminApps,
  refetchInterval: REFRESH_MS,
});
const users = useQuery({ queryKey: keys.adminUsers, queryFn: api.adminUsers });
const server = useQuery({
  queryKey: keys.adminServer,
  queryFn: api.adminServer,
  refetchInterval: REFRESH_MS,
});

// Totals over every running app, from each app's latest 15-second sample.
const totals = computed(() => {
  const list = apps.data.value ?? [];
  let cpu = 0;
  let mem = 0;
  let reserved = 0;
  let running = 0;
  for (const a of list) {
    if (a.desired_state !== "running") continue;
    reserved += a.memory_mb;
    if (a.live) {
      running++;
      cpu += a.live.cpu;
      mem += a.live.memory_mb;
    }
  }
  return { cpu, mem, reserved, running, total: list.length };
});

// Busiest apps first.
const sorted = computed(() =>
  [...(apps.data.value ?? [])].sort(
    (x, y) =>
      (y.live?.cpu ?? -1) - (x.live?.cpu ?? -1) ||
      (y.live?.memory_mb ?? -1) - (x.live?.memory_mb ?? -1),
  ),
);

const owners = computed(
  () => new Map((users.data.value ?? []).map((u) => [u.id, u.name || u.email])),
);

const target = ref<App | null>(null);
const open = computed({
  get: () => target.value !== null,
  set: (v) => {
    if (!v) target.value = null;
  },
});
const stop = useMutation({
  mutationFn: (slug: string) => api.adminStopApp(slug),
  onSuccess: (_r, slug) => {
    toastOk(`Stopped ${slug}`);
    target.value = null;
    void qc.invalidateQueries({ queryKey: keys.adminApps });
  },
  onError: (e) => toastError(e),
});
</script>

<template>
  <div>
    <QueryError
      v-if="apps.isError.value"
      :error="apps.error.value"
      @retry="apps.refetch()"
    />
    <div v-else-if="apps.isPending.value" class="grid gap-2">
      <Skeleton v-for="i in 3" :key="i" class="h-12 w-full" />
    </div>
    <EmptyState
      v-else-if="!apps.data.value?.length"
      title="No apps on this server yet."
    />
    <div v-else class="grid gap-4">
      <div class="grid gap-4 sm:grid-cols-3">
        <StatCard
          title="Apps running"
          :value="`${totals.running} of ${totals.total}`"
          :progress="pct(totals.running, totals.total)"
        />
        <StatCard
          title="CPU used by apps"
          :value="formatPercent(totals.cpu)"
          :sub="`${(totals.cpu / 100).toFixed(2)} CPU cores in use (100% = one core)`"
        />
        <StatCard
          title="Memory used by apps"
          :value="
            server.data.value
              ? `${formatMB(totals.mem)} of ${formatMB(server.data.value.mem_total_mb)}`
              : formatMB(totals.mem)
          "
          :sub="`${formatMB(totals.reserved)} reserved as limits`"
          :progress="
            server.data.value
              ? pct(totals.mem, server.data.value.mem_total_mb)
              : undefined
          "
        />
      </div>
      <div class="overflow-x-auto rounded-xl border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>App</TableHead>
              <TableHead class="hidden sm:table-cell">Owner</TableHead>
              <TableHead>Status</TableHead>
              <TableHead class="text-right">CPU</TableHead>
              <TableHead class="text-right">Memory used</TableHead>
              <TableHead class="w-0" />
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="a in sorted" :key="a.id">
              <TableCell>
                <RouterLink
                  :to="{ name: 'app', params: { slug: a.slug } }"
                  class="font-medium hover:underline"
                  >{{ a.name }}</RouterLink
                >
                <p class="text-xs text-muted-foreground">{{ hostOf(a.url) }}</p>
              </TableCell>
              <TableCell class="hidden sm:table-cell">
{{
                owners.get(a.owner_id) ?? a.owner_id
              }}
</TableCell>
              <TableCell><AppStatusBadge :status="a.status" /></TableCell>
              <TableCell class="text-right tabular-nums">
{{
                a.live ? formatPercent(a.live.cpu) : "-"
              }}
</TableCell>
              <TableCell class="text-right tabular-nums">
                {{ a.live ? formatMB(a.live.memory_mb) : "-" }}
                <span class="text-xs text-muted-foreground"
                  >/ {{ formatMB(a.memory_mb) }}</span
                >
                <p v-if="a.live" class="text-xs text-muted-foreground">
                  {{ formatPercent(pct(a.live.memory_mb, a.memory_mb)) }} of
                  limit
                </p>
              </TableCell>
              <TableCell>
                <Button
                  v-if="a.status === 'running' || a.status === 'deploying'"
                  size="sm"
                  variant="outline"
                  @click="target = a"
                  >
<Square /> Stop
</Button
                >
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </div>

    <ConfirmDialog
      v-model:open="open"
      title="Stop this app?"
      :description="`${target?.name ?? 'This app'} belongs to ${owners.get(target?.owner_id ?? '') ?? 'someone else'}. It goes offline until they start it again.`"
      confirm-label="Stop app"
      :loading="stop.isPending.value"
      @confirm="target && stop.mutate(target.slug)"
    />
  </div>
</template>
