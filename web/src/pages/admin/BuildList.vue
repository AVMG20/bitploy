<script setup lang="ts">
// Recent builds (GET /admin/builds): the builds dialog on Admin > Server
// (everyone's, with who built) and the admin's page of a person.
import RelativeTime from '@/components/RelativeTime.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { buildResultInfo, peakPercent } from '@/lib/builds'
import { formatDuration, formatMB } from '@/lib/format'
import type { BuildRecord } from '@/lib/types'

defineProps<{ builds: BuildRecord[]; showPerson?: boolean }>()
</script>

<template>
  <div class="overflow-x-auto rounded-xl border bg-card">
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>App</TableHead>
          <TableHead class="text-right">Build time</TableHead>
          <TableHead class="hidden text-right sm:table-cell">Waited</TableHead>
          <TableHead>Result</TableHead>
          <TableHead class="hidden text-right md:table-cell">Peak server CPU</TableHead>
          <TableHead class="hidden text-right md:table-cell">Peak memory</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="b in builds" :key="b.deployment_id">
          <TableCell>
            <span class="font-medium" :title="b.app_name">{{ b.app }}</span>
            <span class="block text-xs text-muted-foreground">
              <template v-if="showPerson">{{ b.person || 'A removed user' }} · </template><RelativeTime :date="b.started_at" />
            </span>
          </TableCell>
          <TableCell class="text-right tabular-nums">{{ formatDuration(b.build_seconds) }}</TableCell>
          <TableCell class="hidden text-right tabular-nums sm:table-cell">{{ formatDuration(b.wait_seconds) }}</TableCell>
          <TableCell>
            <StatusBadge v-bind="buildResultInfo(b)" :title="b.error_code ?? undefined" />
          </TableCell>
          <TableCell class="hidden text-right tabular-nums md:table-cell">{{ peakPercent(b.peak_cpu) }}</TableCell>
          <TableCell class="hidden text-right tabular-nums md:table-cell">{{ formatMB(b.peak_memory_mb) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </div>
</template>
