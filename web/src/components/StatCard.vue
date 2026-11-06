<script setup lang="ts">
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

// One number, what it is out of, and a bar for that same fraction. `sub` adds a
// second, smaller fact (like what is in use right now); `alert` says what is wrong and what to do.
defineProps<{ title: string; value?: string; suffix?: string; sub?: string; progress?: number; loading?: boolean; hint?: string; alert?: string }>()
</script>

<template>
  <Card>
    <CardHeader class="pb-1">
      <CardTitle class="text-xs font-medium tracking-wide text-muted-foreground uppercase" :title="hint">{{ title }}</CardTitle>
    </CardHeader>
    <CardContent class="grid gap-2">
      <template v-if="loading">
        <Skeleton class="h-8 w-24" />
        <Skeleton class="h-3 w-32" />
      </template>
      <template v-else>
        <div class="flex flex-wrap items-baseline gap-x-1.5">
          <span class="text-2xl leading-tight sm:text-[1.65rem] font-semibold tracking-tight tabular-nums">{{ value }}</span>
          <span v-if="suffix" class="text-sm whitespace-nowrap text-muted-foreground">{{ suffix }}</span>
        </div>
        <Progress
          v-if="progress !== undefined"
          :model-value="progress"
          :class="cn(progress >= 90 && '[&>[data-slot=progress-indicator]]:bg-destructive', progress >= 75 && progress < 90 && '[&>[data-slot=progress-indicator]]:bg-warning')"
        />
        <p v-if="sub" class="text-xs text-muted-foreground">{{ sub }}</p>
        <p v-if="alert" class="text-xs font-medium text-destructive" role="status">{{ alert }}</p>
      </template>
    </CardContent>
  </Card>
</template>
