<script setup lang="ts">
import { computed } from 'vue'
import { Box, Cog, Database, FolderOpen, Globe, Search, Server, Shuffle, Zap } from 'lucide-vue-next'
import { logoFor } from '@/lib/logos'
import type { RoleKey } from '@/lib/services'
import { cn } from '@/lib/utils'

// The tile in front of a service: what it is for, at a glance. The website (the
// public service) gets the accent; everything else stays neutral, so status
// colours keep meaning status. `logo` is a service template id: its brand mark
// replaces the role icon when there is one.
const props = defineProps<{ role: RoleKey; size?: 'sm' | 'md'; logo?: string }>()
const ICONS = { website: Globe, api: Server, worker: Cog, database: Database, cache: Zap, queue: Shuffle, files: FolderOpen, search: Search, service: Box }
const icon = computed(() => ICONS[props.role])
const mark = computed(() => (props.logo ? logoFor(props.logo) : null))
</script>

<template>
  <span
    :class="cn(
      'flex shrink-0 items-center justify-center rounded-lg border',
      size === 'sm' ? 'size-7' : 'size-9',
      mark ? 'bg-muted/60 text-foreground' : role === 'website' ? 'border-primary/30 bg-primary/10 text-primary-text' : 'bg-muted/60 text-muted-foreground',
    )"
    aria-hidden="true"
  >
    <svg v-if="mark" viewBox="0 0 24 24" :class="size === 'sm' ? 'size-4' : 'size-5'" :fill="mark.color ?? 'currentColor'"><path :d="mark.path" /></svg>
    <component :is="icon" v-else :class="size === 'sm' ? 'size-3.5' : 'size-4'" />
  </span>
</template>
