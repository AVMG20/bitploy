<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronRight } from 'lucide-vue-next'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { useCatalogApp } from '@/composables/queries'
import type { App } from '@/lib/types'

// What to do first in an app that came from the catalog. Whether it is folded stays in this browser.
const props = defineProps<{ app: App }>()
const entry = useCatalogApp(() => props.app.catalog_id)
const steps = computed(() => (entry.data.value?.first_steps ?? []).map((s) => s.replaceAll('{url}', props.app.url)))

const storeKey = computed(() => `bitploy:first-steps:${props.app.id}`)
function read(): boolean {
  try {
    return localStorage.getItem(storeKey.value) !== 'closed'
  } catch {
    return true
  }
}
const open = ref(read())
function setOpen(v: boolean) {
  open.value = v
  try {
    localStorage.setItem(storeKey.value, v ? 'open' : 'closed')
  } catch {
    // Not remembered; it still folds.
  }
}
</script>

<template>
  <Card v-if="steps.length">
    <Collapsible :open="open" @update:open="setOpen">
      <CardHeader>
        <CollapsibleTrigger as-child>
          <button type="button" class="flex w-fit items-center gap-1 text-left">
            <ChevronRight :class="['size-4 text-muted-foreground transition-transform', open && 'rotate-90']" />
            <CardTitle>First steps</CardTitle>
          </button>
        </CollapsibleTrigger>
      </CardHeader>
      <CollapsibleContent>
        <CardContent class="pt-4">
          <ol class="grid gap-2 text-sm">
            <li v-for="(s, i) in steps" :key="i" class="flex gap-3">
              <span class="w-4 shrink-0 text-right font-mono text-xs leading-5 text-muted-foreground">{{ i + 1 }}</span>
              <span class="min-w-0 break-words">{{ s }}</span>
            </li>
          </ol>
        </CardContent>
      </CollapsibleContent>
    </Collapsible>
  </Card>
</template>
