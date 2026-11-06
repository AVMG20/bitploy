<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { deployErrorSummary } from '@/lib/buildError'
import { plainHint } from '@/lib/errors'
import { isPortError } from '@/lib/runSettings'
import { aboutService } from '@/lib/services'
import type { DeployError } from '@/lib/types'

// Why a deploy failed: what happened, the line of the log that says why (the
// compiler message, not Docker's "exit code: 1"), and what to do about it.
// slug: the app, for a link to the setting that fixes the error.
const props = defineProps<{ error: DeployError; logTail?: string[]; multi?: boolean; alert?: boolean; slug?: string }>()

const summary = computed(() => deployErrorSummary(props.error, props.logTail))
const message = computed(() => (props.multi ? aboutService(summary.value.message, props.error.service) : summary.value.message))
// A deploy command that failed: a link to where it is set.
const isHookError = computed(() => props.error.code === 'before_deploy_failed' || props.error.code === 'before_deploy_timeout')
const hint = computed(() => {
  if (isHookError.value && /out of memory/i.test(props.error.message))
    return 'The command gets the memory of the app. Give the app more memory under Settings, Size, or make the command use less, then deploy again.'
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
    <p v-if="slug && !multi && isPortError(error.code)" class="mt-2">
      <RouterLink :to="{ name: 'app-settings', params: { slug }, hash: '#run' }" class="font-medium underline underline-offset-2">Set the port</RouterLink>
    </p>
    <p v-if="slug && !multi && isHookError" class="mt-2">
      <RouterLink :to="{ name: 'app-settings', params: { slug }, hash: '#deploy-commands' }" class="font-medium underline underline-offset-2">Change the deploy commands</RouterLink>
    </p>
    <slot />
  </div>
</template>
