<script setup lang="ts">
import { computed } from 'vue'
import { TriangleAlert } from 'lucide-vue-next'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ApiError } from '@/lib/api'

const props = defineProps<{ error: unknown }>()
defineEmits<{ retry: [] }>()

const message = computed(() => (props.error instanceof Error ? props.error.message : 'Something went wrong.'))
const hint = computed(() => (props.error instanceof ApiError ? props.error.hint : ''))
</script>

<template>
  <Alert variant="destructive">
    <TriangleAlert />
    <AlertTitle>{{ message }}</AlertTitle>
    <AlertDescription>
      <p v-if="hint">{{ hint }}</p>
      <Button class="mt-2" variant="outline" size="sm" @click="$emit('retry')">Try again</Button>
    </AlertDescription>
  </Alert>
</template>
