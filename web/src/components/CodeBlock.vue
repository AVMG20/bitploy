<script setup lang="ts">
import CopyButton from './CopyButton.vue'
import SqlText from './SqlText.vue'

defineProps<{ code: string; what?: string; wrap?: boolean; /** The code is SQL: show it with syntax colours. */ sql?: boolean }>()
</script>

<template>
  <div class="relative rounded-lg border bg-muted/60">
    <div class="absolute top-1.5 right-1.5">
      <CopyButton :value="code" :what="what" variant="secondary" label="Copy" />
    </div>
    <pre :class="['p-3 pr-20 font-mono text-xs leading-relaxed', wrap ? 'whitespace-pre-wrap' : 'overflow-x-auto whitespace-pre']"><code><SqlText v-if="sql" :sql="code" /><template v-else>{{ code }}</template></code></pre>
  </div>
</template>
