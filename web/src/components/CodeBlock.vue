<script setup lang="ts">
import { computed } from 'vue'
import CodeText from './CodeText.vue'
import CopyButton from './CopyButton.vue'
import SqlText from './SqlText.vue'

const props = defineProps<{
  code: string
  what?: string
  wrap?: boolean
  /** The code is SQL: show it with syntax colours. Same as lang="sql". */
  sql?: boolean
  /** The language, for syntax colours. Without one the code is plain text. */
  lang?: 'sql' | 'dockerfile' | 'yaml'
}>()
const language = computed(() => (props.sql ? 'sql' : props.lang))
</script>

<template>
  <div class="relative rounded-lg border bg-muted/60">
    <div class="absolute top-1.5 right-1.5">
      <CopyButton :value="code" :what="what" variant="secondary" label="Copy" />
    </div>
    <pre :class="['p-3 pr-20 font-mono text-xs leading-relaxed', wrap ? 'whitespace-pre-wrap' : 'overflow-x-auto whitespace-pre']"><code><SqlText v-if="language === 'sql'" :sql="code" /><CodeText v-else-if="language" :code="code" :lang="language" /><template v-else>{{ code }}</template></code></pre>
  </div>
</template>
