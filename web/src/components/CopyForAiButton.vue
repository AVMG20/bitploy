<script setup lang="ts">
import { ref } from 'vue'
import { Check, ClipboardCopy, Loader2 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { buildFailureText } from '@/lib/copyForAi'
import { copyTextLater } from '@/lib/notify'
import type { App, Deployment } from '@/lib/types'

// Copies a short description of what failed, with the end of the log, to paste
// into an AI assistant. Without a deployment it describes the running app.
const props = defineProps<{ app: App; deployment?: Deployment | null; variant?: 'outline' | 'ghost' | 'secondary' }>()

const busy = ref(false)
const done = ref(false)

async function copy() {
  if (busy.value) return
  busy.value = true
  try {
    await copyTextLater(() => buildFailureText(props.app, props.deployment), 'Copied. Paste it into your AI assistant.')
    done.value = true
    setTimeout(() => (done.value = false), 2000)
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
