<script setup lang="ts">
import { Textarea } from '@/components/ui/textarea'

defineProps<{ id?: string; placeholder?: string; disabled?: boolean; readonly?: boolean; rows?: number; invalid?: boolean }>()
const model = defineModel<string>({ required: true })
defineEmits<{ blur: [] }>()
</script>

<template>
  <Textarea
    :id="id"
    v-model="model"
    :rows="rows ?? 10"
    :placeholder="placeholder"
    :disabled="disabled"
    :readonly="readonly"
    :aria-invalid="invalid || undefined"
    autocapitalize="off"
    autocomplete="off"
    spellcheck="false"
    wrap="off"
    class="max-h-96 min-h-32 resize-y overflow-auto font-mono text-xs leading-relaxed whitespace-pre md:text-xs"
    @blur="$emit('blur')"
  />
</template>
