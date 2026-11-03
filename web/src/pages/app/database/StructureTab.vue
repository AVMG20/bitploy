<script setup lang="ts">
import { computed } from 'vue'
import { KeyRound, Lock } from 'lucide-vue-next'
import CodeBlock from '@/components/CodeBlock.vue'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { foreignKeys } from '@/lib/sqlSchema'
import type { DbTable } from '@/lib/types'

const props = defineProps<{ table: DbTable }>()

// The schema does not list foreign keys; they are read out of the CREATE statement.
const references = computed(() => foreignKeys(props.table.sql))

const indexSql = computed(() =>
  props.table.indexes
    .map((i) => i.sql)
    .filter((s): s is string => !!s)
    .map((s) => (s.trim().endsWith(';') ? s.trim() : s.trim() + ';'))
    .join('\n'),
)
</script>

<template>
  <div class="grid min-w-0 gap-6">
    <section class="grid min-w-0 gap-2">
      <h2 class="text-sm font-medium">Columns</h2>
      <div class="min-w-0 overflow-x-auto rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Not null</TableHead>
              <TableHead>Default</TableHead>
              <TableHead>Key</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="c in table.columns" :key="c.name">
              <TableCell class="font-mono text-[13px] font-medium">
                {{ c.name }}
                <Lock v-if="c.redacted" class="ml-1 inline size-3 align-middle text-muted-foreground" aria-label="Hidden: the server does not show this column's values" />
                <Badge v-if="c.generated" variant="secondary" class="ml-1.5 align-middle" title="SQLite computes this column from others">generated</Badge>
                <Badge v-else-if="c.hidden" variant="secondary" class="ml-1.5 align-middle" title="Not part of the table's visible columns">hidden</Badge>
              </TableCell>
              <TableCell class="font-mono text-[13px] text-muted-foreground">{{ c.type || 'any' }}</TableCell>
              <TableCell class="text-muted-foreground">{{ c.notnull ? 'Yes' : '' }}</TableCell>
              <TableCell class="font-mono text-[13px]">
                <span v-if="c.default !== null">{{ c.default }}</span>
              </TableCell>
              <TableCell>
                <span v-if="c.pk > 0" class="inline-flex items-center gap-1 text-[13px]">
                  <KeyRound class="size-3.5 text-warning" /> Primary key<template v-if="table.columns.filter((x) => x.pk > 0).length > 1"> ({{ c.pk }})</template>
                </span>
                <span v-if="references[c.name]" class="block font-mono text-xs text-muted-foreground" :class="c.pk > 0 && 'mt-0.5'" title="Foreign key">
                  &rarr; {{ references[c.name]!.table }}<template v-if="references[c.name]!.column">.{{ references[c.name]!.column }}</template>
                </span>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </section>

    <section class="grid min-w-0 gap-2">
      <h2 class="text-sm font-medium">Indexes</h2>
      <p v-if="!table.indexes.length" class="text-sm text-muted-foreground">{{ table.type === 'view' ? 'Views have no indexes.' : 'This table has no indexes.' }}</p>
      <div v-else class="overflow-hidden rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Columns</TableHead>
              <TableHead>Unique</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="i in table.indexes" :key="i.name">
              <TableCell class="font-mono text-[13px] font-medium">
                {{ i.name }}
                <Badge v-if="i.name.startsWith('sqlite_autoindex_')" variant="secondary" class="ml-1.5 align-middle" title="Made by SQLite for a UNIQUE or PRIMARY KEY constraint">auto</Badge>
              </TableCell>
              <TableCell class="font-mono text-[13px] text-muted-foreground">{{ i.columns.join(', ') }}</TableCell>
              <TableCell class="text-muted-foreground">{{ i.unique ? 'Yes' : '—' }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </section>

    <section class="grid min-w-0 gap-2">
      <h2 class="text-sm font-medium">SQL</h2>
      <CodeBlock v-if="table.sql" :code="table.sql" what="SQL copied" sql />
      <p v-else class="text-sm text-muted-foreground">SQLite has no definition for this one.</p>
      <CodeBlock v-if="indexSql" :code="indexSql" what="SQL copied" sql />
    </section>
  </div>
</template>
