<script setup lang="ts">
import { computed } from 'vue'
import { highlightLog, LOG_TOKEN_CLASS, logRowClass } from '@/lib/logHighlight'

// A few log lines (a build log tail, the last lines in a panel) with colours
// for levels, build steps, status codes and stack traces. The full log on the
// Logs tab colours its lines the same way.
const props = defineProps<{ lines: string[] }>()
const rows = computed(() => props.lines.map((text) => highlightLog(text)))
</script>

<template>
  <div v-for="(r, i) in rows" :key="i" :class="['-mx-1 rounded-sm px-1 break-words whitespace-pre-wrap', logRowClass(r)]">
    <template v-for="(t, j) in r.tokens" :key="j"><span v-if="LOG_TOKEN_CLASS[t.type]" :class="LOG_TOKEN_CLASS[t.type]">{{ t.text }}</span><template v-else>{{ t.text }}</template></template><template v-if="!r.tokens.length">&#8203;</template>
  </div>
</template>
