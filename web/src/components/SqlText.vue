<script setup lang="ts">
import { computed } from 'vue'
import { tokenizeSql } from '@/lib/sqlHighlight'
import type { SqlTokenType } from '@/lib/sqlHighlight'

// SQL with syntax colours. Every token is a text node (never HTML), so SQL
// from anywhere is safe to show. The colours are tokens in style.css.
const props = defineProps<{ sql: string }>()
const tokens = computed(() => tokenizeSql(props.sql))

const CLASS: Record<SqlTokenType, string> = {
  keyword: 'text-sql-keyword',
  string: 'text-sql-string',
  identifier: '',
  number: 'text-sql-number',
  comment: 'text-sql-comment',
  operator: 'text-sql-operator',
  punct: 'text-sql-operator',
  text: '',
}
</script>

<template>
  <template v-for="(t, i) in tokens" :key="i"><span v-if="CLASS[t.type]" :class="CLASS[t.type]">{{ t.text }}</span><template v-else>{{ t.text }}</template></template>
</template>
