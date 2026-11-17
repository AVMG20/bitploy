<script setup lang="ts">
import { RefreshCw, RotateCw } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

// Saved changes that the running app does not use yet, with the button that
// applies them. On the Configure pages it sticks to the bottom of the screen
// (`sticky`), so it stays in reach while someone edits a long page.
defineProps<{
  text: string
  needsBuild: boolean
  busy: boolean
  canRebuild: boolean
  restarting: boolean
  redeploying: boolean
  sticky?: boolean
}>()
defineEmits<{ rebuild: []; restart: [] }>()
</script>

<template>
  <div
    role="status"
    :class="cn('flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-warning/40 bg-card px-4 py-3 text-sm', sticky ? 'sticky bottom-4 z-10 shadow-lg' : 'bg-warning/10')"
    data-testid="pending-changes"
  >
    <span class="flex size-8 shrink-0 items-center justify-center rounded-lg bg-warning/15 text-warning-text"><RotateCw class="size-4" aria-hidden="true" /></span>
    <p class="min-w-0 flex-1">
      <span class="font-medium">{{ needsBuild ? 'Deploy again to apply your changes.' : 'Restart to apply your changes.' }}</span>
      <span class="text-muted-foreground"> Your changes to {{ text }} are saved, but the running app does not use them yet.</span>
    </p>
    <Button v-if="needsBuild" size="sm" :disabled="busy || !canRebuild" @click="$emit('rebuild')">
      <RefreshCw :class="redeploying && 'animate-spin'" /> Deploy again
    </Button>
    <Button v-else size="sm" :disabled="busy" @click="$emit('restart')">
      <RotateCw :class="restarting && 'animate-spin'" /> {{ restarting ? 'Restarting...' : 'Restart now' }}
    </Button>
  </div>
</template>
