<script setup lang="ts">
import { computed } from 'vue'
import { compactCount, formatCount } from '@/lib/format'

// A count shown short from 10,000 ("12.3k", "1.2m"), with the exact number on
// hover and for screen readers.
const props = defineProps<{ n: number }>()
const short = computed(() => compactCount(props.n))
const full = computed(() => formatCount(props.n))
</script>

<template>
  <template v-if="short === full">{{ full }}</template>
  <span v-else :title="full"><span aria-hidden="true">{{ short }}</span><span class="sr-only">{{ full }}</span></span>
</template>
