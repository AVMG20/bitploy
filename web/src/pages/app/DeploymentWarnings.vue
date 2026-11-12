<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { ChevronRight } from 'lucide-vue-next'
import { plainHint } from '@/lib/errors'
import type { Finding } from '@/lib/types'

const props = defineProps<{ warnings: Finding[]; slug: string }>()

// Three or more fold away behind one line, so a working version does not
// read like a wall of problems.
const COLLAPSE_AT = 3

// Warnings first, then the tips.
const sorted = computed(() => [...props.warnings].sort((a, b) => Number(a.severity === 'info') - Number(b.severity === 'info')))
const collapsed = computed(() => sorted.value.length >= COLLAPSE_AT)
const warnCount = computed(() => sorted.value.filter((w) => w.severity !== 'info').length)
const tipCount = computed(() => sorted.value.length - warnCount.value)
// A failed after-deploy command needs the user, so its list starts open.
const startOpen = computed(() => sorted.value.some((w) => w.code === 'after_deploy_failed'))

const summary = computed(() => {
  const parts: string[] = []
  if (warnCount.value) parts.push(`${warnCount.value} ${warnCount.value === 1 ? 'warning' : 'warnings'}`)
  if (tipCount.value) parts.push(`${tipCount.value} ${tipCount.value === 1 ? 'tip' : 'tips'}`)
  return parts.join(' and ')
})
</script>

<template>
  <details v-if="collapsed" class="group rounded-lg border" :open="startOpen || undefined">
    <summary
      class="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-sm select-none [&::-webkit-details-marker]:hidden"
      :class="warnCount ? 'text-warning-text hover:opacity-80' : 'text-muted-foreground hover:text-foreground'"
    >
      <ChevronRight class="size-4 transition-transform group-open:rotate-90" aria-hidden="true" />
      {{ summary }}
    </summary>
    <ul class="grid gap-1 border-t px-3 py-2 text-sm">
      <li v-for="(w, i) in sorted" :key="i" :class="w.severity === 'info' ? 'text-muted-foreground' : 'text-warning-text'">
        {{ w.message }}<template v-if="plainHint(w.code, w.hint)">&nbsp;{{ plainHint(w.code, w.hint) }}</template>
        <RouterLink
          v-if="w.code === 'after_deploy_failed'"
          :to="{ name: 'app-settings', params: { slug }, hash: '#deploy-commands' }"
          class="ml-1 font-medium underline underline-offset-2"
        >
          Change the deploy commands
        </RouterLink>
      </li>
    </ul>
  </details>
  <ul v-else class="grid gap-1 text-sm">
    <li v-for="(w, i) in sorted" :key="i" :class="w.severity === 'info' ? 'text-muted-foreground' : 'text-warning-text'">
      {{ w.message }}<template v-if="plainHint(w.code, w.hint)">&nbsp;{{ plainHint(w.code, w.hint) }}</template>
      <RouterLink
        v-if="w.code === 'after_deploy_failed'"
        :to="{ name: 'app-settings', params: { slug }, hash: '#deploy-commands' }"
        class="ml-1 font-medium underline underline-offset-2"
      >
        Change the deploy commands
      </RouterLink>
    </li>
  </ul>
</template>
