<script setup lang="ts">
import { ref, watch } from 'vue'
import { Check, ClipboardCopy, Loader2 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { buildFailureText } from '@/lib/copyForAi'
import { copyText, copyTextLater } from '@/lib/notify'
import type { App, Deployment } from '@/lib/types'

// Copies a short description of what failed, with the end of the log, to paste
// into an AI assistant. Without a deployment it describes the running app; with
// `text` (a whole project) it copies that.
const props = defineProps<{ app?: App; deployment?: Deployment | null; text?: string; variant?: 'outline' | 'ghost' | 'secondary' }>()

const busy = ref(false)
const done = ref(false)
// The last text, kept briefly: if the browser refused the first copy (plain
// http), a second click copies it at once, inside the click.
let cached: { text: string; at: number } | null = null
watch(
  () => [props.app?.id, props.app?.status, props.deployment?.id, props.text],
  () => (cached = null),
)

const what = 'Copied. Paste it into your AI assistant.'

async function copy() {
  if (busy.value) return
  busy.value = true
  try {
    let ok: boolean
    if (props.text !== undefined || !props.app) {
      ok = await copyText(props.text ?? '', what)
    } else if (cached && Date.now() - cached.at < 60_000) {
      ok = await copyText(cached.text, what)
    } else {
      const text = buildFailureText(props.app, props.deployment).then((t) => {
        cached = { text: t, at: Date.now() }
        return t
      })
      ok = await copyTextLater(() => text, what)
    }
    if (ok) {
      done.value = true
      setTimeout(() => (done.value = false), 2000)
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Button type="button" size="sm" :variant="variant ?? 'outline'" :disabled="busy" @click="copy">
    <Loader2 v-if="busy" class="animate-spin" />
    <Check v-else-if="done" />
    <ClipboardCopy v-else />
    {{ done ? 'Copied' : 'Copy for AI' }}
  </Button>
</template>
