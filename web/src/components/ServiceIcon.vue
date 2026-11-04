<script setup lang="ts">
import { computed } from 'vue'
import { Box, Cog, Database, FolderOpen, Globe, Search, Server, Shuffle, Zap } from 'lucide-vue-next'
import type { RoleKey } from '@/lib/services'
import { cn } from '@/lib/utils'

// The tile in front of a service: what it is for, at a glance. The website (the
// public service) gets the accent; everything else stays neutral, so status
// colours keep meaning status.
const props = defineProps<{ role: RoleKey; size?: 'sm' | 'md' }>()
const ICONS = { website: Globe, api: Server, worker: Cog, database: Database, cache: Zap, queue: Shuffle, files: FolderOpen, search: Search, service: Box }
const icon = computed(() => ICONS[props.role])
</script>

<template>
  <span
    :class="cn(
      'flex shrink-0 items-center justify-center rounded-lg border',
      size === 'sm' ? 'size-7' : 'size-9',
      role === 'website' ? 'border-primary/30 bg-primary/10 text-primary' : 'bg-muted/60 text-muted-foreground',
    )"
    aria-hidden="true"
  >
    <component :is="icon" :class="size === 'sm' ? 'size-3.5' : 'size-4'" />
  </span>
</template>
