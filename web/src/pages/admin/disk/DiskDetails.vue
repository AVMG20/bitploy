<script setup lang="ts">
// The largest images, containers and volumes, and bitploy's own files.
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import RelativeTime from '@/components/RelativeTime.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { appPath } from '@/lib/appLinks'
import { CONTAINER_CATEGORIES, FILES, IMAGE_CATEGORIES, share, VOLUME_CATEGORIES } from '@/lib/disk'
import { formatBytes } from '@/lib/format'
import type { DiskReport } from '@/lib/types'

const LIMIT = 12
const props = defineProps<{ report: DiskReport }>()

const tab = ref('images')
const all = ref(false)
const cut = <T,>(rows: T[]) => (all.value ? rows : rows.slice(0, LIMIT))
const rowsOf = computed(() => ({ images: props.report.images.length, containers: props.report.containers.length, volumes: props.report.volumes.length }))
const more = computed(() => tab.value !== 'files' && rowsOf.value[tab.value as keyof typeof rowsOf.value] > LIMIT)
const files = computed(() => props.report.files.filter((f) => f.bytes > 0))
const filesMax = computed(() => Math.max(1, ...files.value.map((f) => f.bytes)))

function badge(category: string): 'default' | 'secondary' | 'outline' | 'destructive' {
  if (category === 'in_use' || category === 'running' || category === 'app') return 'default'
  if (category === 'orphan') return 'destructive'
  if (category === 'other') return 'outline'
  return 'secondary'
}
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Largest items</CardTitle>
    </CardHeader>
    <CardContent>
      <Tabs v-model="tab" @update:model-value="all = false">
        <TabsList>
          <TabsTrigger value="images">Images</TabsTrigger>
          <TabsTrigger value="containers">Containers</TabsTrigger>
          <TabsTrigger value="volumes">Volumes</TabsTrigger>
          <TabsTrigger value="files">bitploy files</TabsTrigger>
        </TabsList>

        <TabsContent value="images" class="pt-3">
          <p v-if="report.images.length === 0" class="py-8 text-center text-sm text-muted-foreground">No images.</p>
          <Table v-else>
            <TableHeader>
              <TableRow class="hover:bg-transparent">
                <TableHead>Image</TableHead>
                <TableHead class="hidden sm:table-cell">App</TableHead>
                <TableHead>Status</TableHead>
                <TableHead class="hidden md:table-cell">Made</TableHead>
                <TableHead class="text-right">Size</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="im in cut(report.images)" :key="im.id">
                <TableCell class="max-w-36 sm:max-w-72">
                  <p class="truncate font-mono text-xs" :title="im.tags.join('\n')">{{ im.tags[0] ?? 'untagged' }}</p>
                  <p class="truncate font-mono text-[11px] text-muted-foreground">{{ im.id }}<template v-if="im.tags.length > 1"> · +{{ im.tags.length - 1 }} tags</template></p>
                </TableCell>
                <TableCell class="hidden sm:table-cell">
                  <RouterLink v-if="im.app" :to="appPath(im.app.slug)" class="hover:underline">{{ im.app.name || im.app.slug }}</RouterLink>
                  <span v-else class="text-muted-foreground">-</span>
                </TableCell>
                <TableCell><Badge :variant="badge(im.category)">{{ IMAGE_CATEGORIES[im.category] }}</Badge></TableCell>
                <TableCell class="hidden text-muted-foreground md:table-cell"><RelativeTime :date="im.created_at" /></TableCell>
                <TableCell class="text-right whitespace-nowrap tabular-nums" :title="`${formatBytes(im.size_bytes)} including layers shared with other images`">{{ formatBytes(im.unique_bytes) }}</TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </TabsContent>

        <TabsContent value="containers" class="pt-3">
          <p v-if="report.containers.length === 0" class="py-8 text-center text-sm text-muted-foreground">No stopped containers, and none that wrote files of their own.</p>
          <Table v-else>
            <TableHeader>
              <TableRow class="hover:bg-transparent">
                <TableHead>Container</TableHead>
                <TableHead class="hidden sm:table-cell">App</TableHead>
                <TableHead>Status</TableHead>
                <TableHead class="text-right">Files</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="c in cut(report.containers)" :key="c.name">
                <TableCell class="max-w-36 sm:max-w-72">
                  <p class="truncate font-mono text-xs">{{ c.name }}</p>
                  <p class="text-[11px] text-muted-foreground">{{ c.state }}</p>
                </TableCell>
                <TableCell class="hidden sm:table-cell">
                  <RouterLink v-if="c.app" :to="appPath(c.app.slug)" class="hover:underline">{{ c.app.name || c.app.slug }}</RouterLink>
                  <span v-else class="text-muted-foreground">-</span>
                </TableCell>
                <TableCell><Badge :variant="badge(c.category)">{{ CONTAINER_CATEGORIES[c.category] }}</Badge></TableCell>
                <TableCell class="text-right whitespace-nowrap tabular-nums">{{ formatBytes(c.bytes) }}</TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </TabsContent>

        <TabsContent value="volumes" class="pt-3">
          <p v-if="report.volumes.length === 0" class="py-8 text-center text-sm text-muted-foreground">No volumes.</p>
          <Table v-else>
            <TableHeader>
              <TableRow class="hover:bg-transparent">
                <TableHead>Volume</TableHead>
                <TableHead class="hidden sm:table-cell">App</TableHead>
                <TableHead>Kind</TableHead>
                <TableHead class="text-right">Size</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="v in cut(report.volumes)" :key="v.name">
                <TableCell class="max-w-36 sm:max-w-72">
                  <p class="truncate font-mono text-xs" :title="v.name">{{ v.name }}</p>
                  <p class="text-[11px] text-muted-foreground">{{ v.in_use ? 'Mounted' : 'Not mounted' }}</p>
                </TableCell>
                <TableCell class="hidden sm:table-cell">
                  <RouterLink v-if="v.app" :to="appPath(v.app.slug)" class="hover:underline">{{ v.app.name || v.app.slug }}</RouterLink>
                  <span v-else class="text-muted-foreground">-</span>
                </TableCell>
                <TableCell><Badge :variant="badge(v.category)">{{ VOLUME_CATEGORIES[v.category] }}</Badge></TableCell>
                <TableCell class="text-right whitespace-nowrap tabular-nums">{{ v.bytes < 0 ? '-' : formatBytes(v.bytes) }}</TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </TabsContent>

        <TabsContent value="files" class="pt-3">
          <p v-if="files.length === 0" class="py-8 text-center text-sm text-muted-foreground">No files yet.</p>
          <ul v-else class="grid gap-3 py-1">
            <li v-for="f in files" :key="f.key" class="grid gap-1.5">
              <div class="flex items-baseline justify-between gap-3 text-sm">
                <span>{{ FILES[f.key] }}</span>
                <span class="font-medium tabular-nums">{{ formatBytes(f.bytes) }}</span>
              </div>
              <div class="h-1.5 rounded-full bg-muted">
                <div class="h-full rounded-full bg-chart-4" :style="{ width: `${Math.max(1, share(f.bytes, filesMax))}%` }" />
              </div>
            </li>
          </ul>
        </TabsContent>
      </Tabs>
      <div v-if="more" class="mt-3 flex justify-center">
        <Button variant="ghost" size="sm" @click="all = !all">{{ all ? 'Show fewer' : 'Show all' }}</Button>
      </div>
    </CardContent>
  </Card>
</template>
