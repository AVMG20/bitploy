<script lang="ts" setup>
import type { HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'
import { useFormField } from './useFormField'

const props = defineProps<{
  class?: HTMLAttributes['class']
}>()

// The field's own error, like FormLabel and FormControl. Not the form's error
// for the name (vee-validate's ErrorMessage): that also holds what the form's
// first check found while the field was not shown yet, such as "Required" for
// a form that appears once a query has loaded.
const { error, formMessageId } = useFormField()
</script>

<template>
  <p v-if="error" :id="formMessageId" role="alert" data-slot="form-message" :class="cn('text-destructive text-sm', props.class)">{{ error }}</p>
</template>
