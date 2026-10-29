<script setup lang="ts">
import { computed } from 'vue'
import { exactTime, relativeTime } from '@/lib/format'
import { nowMs } from '@/composables/useNow'

const props = defineProps<{ date: string | null | undefined; empty?: string }>()
const text = computed(() => (props.date ? relativeTime(props.date, nowMs.value) : (props.empty ?? 'never')))
</script>

<template>
  <time :datetime="date ?? undefined" :title="exactTime(date)" class="whitespace-nowrap">{{ text }}</time>
</template>
