<script setup lang="ts">
import { computed } from 'vue'
import CopyButton from '@/components/CopyButton.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { copyableText, fullView, isBlob, isLongText, sizeLabel } from '@/lib/dbvalue'
import { copyTextLater, toastError } from '@/lib/notify'
import { Copy } from 'lucide-vue-next'
import type { DbValue } from '@/lib/types'

const props = defineProps<{ value: DbValue; column: string; /** Fetches the whole value of a text that is shown cut. */ full?: () => Promise<DbValue> }>()
const open = defineModel<boolean>('open', { required: true })

const view = computed(() => fullView(props.value))
const note = computed(() => {
  const v = view.value
  if (v.truncated && v.size !== null) {
    const shown = v.hex ? '4 KB' : '64 KB'
    return `Showing the first ${shown} of ${sizeLabel(v.size)}.`
  }
  if (v.size !== null) return sizeLabel(v.size)
  return ''
})

// A cut text: copy the whole of it when it can be fetched, else say that only the start is copied.
const cut = computed(() => isLongText(props.value))
async function copyWhole() {
  const shown = copyableText(props.value)
  await copyTextLater(async () => {
    try {
      const v = await props.full!()
      if (typeof v === 'string') return v
    } catch (e) {
      toastError(e)
    }
    return shown
  }, 'Copied')
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-3xl">
      <DialogHeader>
        <DialogTitle class="font-mono text-sm">{{ column }}</DialogTitle>
        <DialogDescription>
          <span class="uppercase">{{ view.kind }}</span><template v-if="note"> &middot; {{ note }}</template>
        </DialogDescription>
      </DialogHeader>
      <pre class="max-h-[60vh] overflow-auto rounded-lg border bg-muted/60 p-3 font-mono text-xs leading-relaxed" :class="view.hex ? 'whitespace-pre' : 'break-words whitespace-pre-wrap'">{{ view.text }}</pre>
      <div class="flex justify-end">
        <Button v-if="cut && full" variant="outline" size="sm" @click="copyWhole"><Copy /> Copy</Button>
        <CopyButton v-else :value="copyableText(value)" :what="isBlob(value) ? 'Base64 copied' : 'Value copied'" variant="outline" :label="isBlob(value) ? 'Copy base64' : cut ? 'Copy first 64 KB' : 'Copy'" />
      </div>
    </DialogContent>
  </Dialog>
</template>
