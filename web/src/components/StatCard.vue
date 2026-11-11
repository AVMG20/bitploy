<script setup lang="ts">
import { ChevronRight } from 'lucide-vue-next'
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

// One number, what it is out of, and a bar for that same fraction. `sub` adds a
// second, smaller fact (like what is in use right now); `alert` says what is wrong and what to do.
// With `to` the whole card is a link to a page with the details.
defineProps<{ title: string; value?: string; suffix?: string; sub?: string; progress?: number; loading?: boolean; hint?: string; alert?: string; to?: RouteLocationRaw }>()
</script>

<template>
  <component
    :is="to ? RouterLink : 'div'"
    :to="to"
    :class="to && 'group/stat block rounded-xl outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50'"
  >
    <Card :class="cn('h-full', to && 'transition-colors group-hover/stat:border-primary/40 group-hover/stat:bg-muted/30')">
      <CardHeader class="pb-1">
        <CardTitle class="flex items-center justify-between gap-2 text-xs font-medium tracking-wide text-muted-foreground uppercase" :title="hint">
          {{ title }}
          <ChevronRight
            v-if="to"
            class="size-4 shrink-0 text-muted-foreground/60 transition-transform group-hover/stat:translate-x-0.5 group-hover/stat:text-primary"
            aria-hidden="true"
          />
        </CardTitle>
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
  </component>
</template>
