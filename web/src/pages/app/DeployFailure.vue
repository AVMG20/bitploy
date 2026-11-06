<script setup lang="ts">
import { computed } from 'vue'
import { deployErrorSummary } from '@/lib/buildError'
import { plainHint } from '@/lib/errors'
import { aboutService } from '@/lib/services'
import type { DeployError } from '@/lib/types'

// Why a deploy failed: what happened, the line of the log that says why (the
// compiler message, not Docker's "exit code: 1"), and what to do about it.
const props = defineProps<{ error: DeployError; logTail?: string[]; multi?: boolean; alert?: boolean }>()

const summary = computed(() => deployErrorSummary(props.error, props.logTail))
const message = computed(() => (props.multi ? aboutService(summary.value.message, props.error.service) : summary.value.message))
const hint = computed(() => {
  const h = plainHint(props.error.code, props.error.hint)
  if (h) return h
  if (props.error.code === 'build_failed') return 'Fix this in your code or Dockerfile, then deploy again.'
  if (props.error.code === 'app_crashed') return 'Fix this in your code, then deploy again. If it names a missing variable, add it under Variables.'
  return ''
})
</script>

<template>
  <div class="rounded-lg bg-destructive/10 p-3 text-sm text-destructive" :role="alert ? 'alert' : undefined">
    <p class="font-medium">{{ message }}</p>
    <p v-if="summary.line" class="mt-2 rounded-md border border-destructive/25 bg-background/70 px-2.5 py-1.5 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap text-foreground">{{ summary.line }}</p>
    <p v-if="hint" class="mt-2">{{ hint }}</p>
    <slot />
  </div>
</template>
