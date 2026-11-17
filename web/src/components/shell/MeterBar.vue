<script setup lang="ts">
import { computed } from 'vue'
import type { UsageBar } from '@/lib/usage'

// A thin bar for one limit: calm while there is room, amber near the limit, red over it.
const props = defineProps<{ pct: number; level: UsageBar['level']; label: string }>()
const tone = computed(() => (props.level === 'ok' ? 'bg-primary' : props.level === 'over' ? 'bg-destructive' : 'bg-warning'))
</script>

<template>
  <div role="img" :aria-label="label" class="h-1.5 w-full overflow-hidden rounded-full bg-muted">
    <div :class="['h-full rounded-full transition-[width]', tone]" :style="{ width: `${pct}%` }" />
  </div>
</template>
