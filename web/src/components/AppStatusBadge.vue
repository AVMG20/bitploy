<script setup lang="ts">
import { computed } from 'vue'
import { appStatusInfo } from '@/lib/format'
import StatusBadge from './StatusBadge.vue'

const props = defineProps<{ status: string }>()
const info = computed(() => appStatusInfo(props.status))
</script>

<template>
  <StatusBadge :label="info.label" :tone="info.tone" />
</template>
