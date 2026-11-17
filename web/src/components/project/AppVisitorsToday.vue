<script setup lang="ts">
import { useAppVisitorsToday } from '@/composables/queries'
import { formatCount } from '@/lib/format'

// Today's visitors of one web app. A component of its own so that apps nobody
// visits (databases, workers) never ask for traffic.
const props = defineProps<{ slug: string }>()
const traffic = useAppVisitorsToday(() => props.slug)
</script>

<template>
  <span v-if="traffic.data.value">{{ formatCount(traffic.data.value.visitors_today) }}</span>
  <span v-else class="text-muted-foreground">-</span>
</template>
