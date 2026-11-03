<script setup lang="ts">
import { TriangleAlert } from 'lucide-vue-next'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { friendlyError } from '@/lib/errors'

// A refresh failed while older data is still on screen: say so without hiding the data.
defineProps<{ error: unknown }>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <Alert variant="destructive">
    <TriangleAlert />
    <AlertTitle>The latest refresh failed: {{ friendlyError(error).message }}</AlertTitle>
    <AlertDescription>
      <p v-if="friendlyError(error).hint">{{ friendlyError(error).hint }}</p>
      <p class="text-muted-foreground">What you see may be out of date.</p>
      <Button class="mt-2" variant="outline" size="sm" @click="$emit('retry')">Try again</Button>
    </AlertDescription>
  </Alert>
</template>
