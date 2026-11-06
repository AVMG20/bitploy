<script setup lang="ts">
import { computed } from 'vue'
import { tokenizeCode } from '@/lib/codeHighlight'
import type { CodeLang, CodeTokenType } from '@/lib/codeHighlight'

// A Dockerfile or compose file with syntax colours. Like SqlText, every token
// is a text node (never HTML). The colours are tokens in style.css.
const props = defineProps<{ code: string; lang: CodeLang }>()
const tokens = computed(() => tokenizeCode(props.code, props.lang))

const CLASS: Record<CodeTokenType, string> = {
  keyword: 'text-sql-keyword',
  key: 'text-code-key',
  string: 'text-sql-string',
  number: 'text-sql-number',
  comment: 'text-sql-comment italic',
  variable: 'text-code-variable',
  anchor: 'text-code-variable',
  operator: 'text-sql-operator',
  punct: 'text-sql-operator',
  text: '',
}
</script>

<template>
  <template v-for="(t, i) in tokens" :key="i"><span v-if="CLASS[t.type]" :class="CLASS[t.type]">{{ t.text }}</span><template v-else>{{ t.text }}</template></template>
</template>
