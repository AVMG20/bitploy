<script setup lang="ts">
import CopyButton from './CopyButton.vue'

defineProps<{ code: string; what?: string; wrap?: boolean }>()
</script>

<template>
  <div class="relative rounded-lg border bg-muted/50">
    <div class="absolute top-1.5 right-1.5">
      <CopyButton :value="code" :what="what" variant="secondary" label="Copy" />
    </div>
    <pre :class="['p-3 pr-20 font-mono text-xs leading-relaxed', wrap ? 'whitespace-pre-wrap' : 'overflow-x-auto whitespace-pre']"><code>{{ code }}</code></pre>
  </div>
</template>
