<script setup lang="ts">
import { provide } from 'vue'
import { FormContextKey, type FormContext } from 'vee-validate'

// useForm provides its form to the whole component, so with several forms in
// one page every FormField would bind to the last one. FormScope gives the
// fields inside it their own form.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const props = defineProps<{ form: FormContext<any> }>()
// eslint-disable-next-line @typescript-eslint/no-explicit-any
provide(FormContextKey, props.form as any)
</script>

<template>
  <slot />
</template>
