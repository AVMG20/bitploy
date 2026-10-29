<script setup lang="ts">
import { ref } from 'vue'
import { Check, Copy } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { copyText } from '@/lib/notify'

const props = withDefaults(
  defineProps<{ value: string; label?: string; what?: string; variant?: 'ghost' | 'outline' | 'secondary'; icon?: boolean }>(),
  { variant: 'ghost', icon: false },
)
const done = ref(false)

async function copy() {
  await copyText(props.value, props.what ?? 'Copied to clipboard')
  done.value = true
  setTimeout(() => (done.value = false), 1500)
}
</script>

<template>
  <Button type="button" :variant="variant" :size="label && !icon ? 'sm' : 'icon-sm'" :aria-label="label ?? 'Copy'" @click="copy">
    <component :is="done ? Check : Copy" />
    <span v-if="label && !icon">{{ done ? 'Copied' : label }}</span>
  </Button>
</template>
