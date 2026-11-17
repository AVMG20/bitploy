<script setup lang="ts">
import { ref } from 'vue'
import { Check, Loader2, Sparkles } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { buildFailureText } from '@/lib/copyForAi'
import { copyTextLater } from '@/lib/notify'
import type { App, Deployment } from '@/lib/types'

// "Ask AI to fix it": copies what failed (the app, the error and the end of the
// log) as a prompt for the assistant that usually did the deploy. The same text
// as the Copy for AI button, with the words of the failed-deploy card.
const props = defineProps<{ app: App; deployment: Deployment; variant?: 'default' | 'outline' }>()

const busy = ref(false)
const done = ref(false)

async function copy() {
  if (busy.value) return
  busy.value = true
  try {
    const ok = await copyTextLater(() => buildFailureText(props.app, props.deployment), 'Copied. Paste it into your AI assistant.')
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
  <Button type="button" :variant="variant ?? 'default'" :disabled="busy" @click="copy">
    <Loader2 v-if="busy" class="animate-spin" />
    <Check v-else-if="done" />
    <Sparkles v-else />
    {{ done ? 'Copied' : 'Ask AI to fix it' }}
  </Button>
</template>
