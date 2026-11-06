<script setup lang="ts">
import { computed, ref } from 'vue'
import CodeText from '@/components/CodeText.vue'
import { Textarea } from '@/components/ui/textarea'
import { looksLikeCompose } from '@/lib/codeHighlight'
import type { CodeLang } from '@/lib/codeHighlight'
import { cn } from '@/lib/utils'

const props = defineProps<{
  id?: string
  placeholder?: string
  disabled?: boolean
  readonly?: boolean
  rows?: number
  invalid?: boolean
  /** The colours to use; without it a text with a top-level services: key is taken for a compose file. */
  lang?: CodeLang
}>()
const model = defineModel<string>({ required: true })
defineEmits<{ blur: [] }>()

const language = computed<CodeLang>(() => props.lang ?? (looksLikeCompose(model.value) ? 'yaml' : 'dockerfile'))

// The coloured copy sits under the textarea, which has transparent text: same
// font, padding and scroll position (like the SQL console).
const underlay = ref<HTMLElement | null>(null)
function syncScroll(e: Event) {
  const el = e.target as HTMLElement
  if (!underlay.value) return
  underlay.value.scrollTop = el.scrollTop
  underlay.value.scrollLeft = el.scrollLeft
}
</script>

<template>
  <div :class="cn('relative rounded-lg bg-card dark:bg-input/30', disabled && 'bg-input/50 opacity-50 dark:bg-input/80')">
    <!-- The code's extra room (pr-6 pb-6) lets the copy scroll as far as the textarea, whose scrollbars take space. -->
    <pre
      ref="underlay"
      aria-hidden="true"
      class="pointer-events-none absolute inset-0 overflow-hidden rounded-lg border border-transparent px-2.5 py-2 font-mono text-xs leading-relaxed whitespace-pre [font-variant-ligatures:none]"
    ><code class="inline-block min-w-full pr-6 pb-6"><CodeText :code="model" :lang="language" />{{ model.endsWith('\n') ? '\u200b' : '' }}</code></pre>
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
      class="relative max-h-96 min-h-32 resize-y overflow-auto bg-transparent font-mono text-xs leading-relaxed whitespace-pre text-transparent caret-foreground selection:bg-primary/25 selection:text-transparent disabled:bg-transparent disabled:opacity-100 md:text-xs dark:bg-transparent dark:disabled:bg-transparent [font-variant-ligatures:none]"
      @scroll="syncScroll"
      @blur="$emit('blur')"
    />
  </div>
</template>
