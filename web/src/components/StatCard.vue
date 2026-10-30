<script setup lang="ts">
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'

defineProps<{ title: string; value?: string; sub?: string; progress?: number; loading?: boolean }>()
</script>

<template>
  <Card>
    <CardHeader class="pb-1">
      <CardTitle class="text-xs font-medium tracking-wide text-muted-foreground uppercase">{{ title }}</CardTitle>
    </CardHeader>
    <CardContent class="grid gap-2">
      <template v-if="loading">
        <Skeleton class="h-8 w-24" />
        <Skeleton class="h-3 w-32" />
      </template>
      <template v-else>
        <div class="text-[1.65rem] leading-tight font-semibold tracking-tight tabular-nums">{{ value }}</div>
        <Progress v-if="progress !== undefined" :model-value="progress" />
        <p v-if="sub" class="text-xs text-muted-foreground">{{ sub }}</p>
      </template>
    </CardContent>
  </Card>
</template>
