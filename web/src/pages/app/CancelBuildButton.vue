<script setup lang="ts">
import { X } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { useAppActions } from '@/composables/useAppActions'
import { canCancelDeployment } from '@/lib/format'
import type { Deployment } from '@/lib/types'

// Stops a build that is still going. Hidden once it is going live (or has ended).
const props = defineProps<{ slug: string; deployment: Deployment }>()
const { cancel } = useAppActions(() => props.slug)
</script>

<template>
  <Button v-if="canCancelDeployment(deployment.state)" size="sm" variant="outline" :disabled="cancel.isPending.value" @click="cancel.mutate(deployment.id)">
    <X /> {{ cancel.isPending.value ? 'Cancelling…' : 'Cancel' }}
  </Button>
</template>
