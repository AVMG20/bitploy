<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowRight } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { deployErrorSummary } from '@/lib/buildError'
import { plainHint } from '@/lib/errors'
import { isPortError } from '@/lib/runSettings'
import { aboutService } from '@/lib/services'
import type { DeployError } from '@/lib/types'

// Why a deploy failed: what happened (the one line in red), the line of the log
// that says why (the compiler message, not Docker's "exit code: 1"), and what to
// do about it. slug: the app, for a button to the setting that fixes the error.
// liveKept: the app still runs the version from before this deploy.
// Slots: `actions` joins the buttons, the default slot follows them.
const props = defineProps<{ error: DeployError; logTail?: string[]; multi?: boolean; alert?: boolean; slug?: string; liveKept?: boolean }>()

// The server's words that the earlier version still runs; said in their own line.
const KEPT = / ?The new version was not started; the version that was live keeps running\./

const summary = computed(() => deployErrorSummary(props.error, props.logTail))
const message = computed(() => {
  const m = summary.value.message
  return props.multi ? aboutService(m, props.error.service) : m
})
const shownMessage = computed(() => message.value.replace(KEPT, ''))
const kept = computed(() => props.liveKept || KEPT.test(message.value))
const isHookError = computed(() => props.error.code === 'before_deploy_failed' || props.error.code === 'before_deploy_timeout' || props.error.code === 'after_deploy_failed')
// Only what the buttons below do not already say.
const hint = computed(() => {
  const { code, message: text } = props.error
  if (isHookError.value && /out of memory/i.test(text)) return 'The command gets the memory of the app. Give the app more memory, or make the command use less.'
  if (code === 'before_deploy_timeout') return 'A command that waits for a database it cannot reach hangs. Check the variables it uses, like DATABASE_URL.'
  if (isHookError.value) return ''
  if (code === 'port_unknown') return 'Or add one EXPOSE line with that port to the Dockerfile. An app without a web address is a Worker.'
  if (code === 'port_ambiguous') return 'Or keep only that EXPOSE line in the Dockerfile.'
  if (code === 'build_failed') return ''
  if (code === 'app_crashed') return 'If it names a missing variable, add it under Variables.'
  return plainHint(code, props.error.hint)
})
const fix = computed(() => {
  if (!props.slug || props.multi) return null
  if (isPortError(props.error.code)) return { label: 'Set the port', hash: '#run' }
  if (isHookError.value) return { label: 'Change deploy commands', hash: '#deploy-commands' }
  return null
})
</script>

<template>
  <div class="rounded-lg border border-l-2 border-l-destructive bg-muted/30 p-3 text-sm" :role="alert ? 'alert' : undefined">
    <p class="font-medium text-destructive">{{ shownMessage }}</p>
    <p v-if="kept" class="mt-1 text-muted-foreground">The version that was live keeps running.</p>
    <p v-if="summary.line" class="mt-2 rounded-md border bg-background/70 px-2.5 py-1.5 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap text-foreground">{{ summary.line }}</p>
    <p v-if="hint" class="mt-2 text-muted-foreground">{{ hint }}</p>
    <div v-if="fix || $slots.actions" class="mt-3 flex flex-wrap items-center gap-2">
      <Button v-if="fix && slug" as-child size="sm" variant="outline">
        <RouterLink :to="{ name: 'app-settings', params: { slug }, hash: fix.hash }">{{ fix.label }} <ArrowRight /></RouterLink>
      </Button>
      <slot name="actions" />
    </div>
    <slot />
  </div>
</template>
