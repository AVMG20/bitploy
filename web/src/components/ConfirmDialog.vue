<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

const props = withDefaults(
  defineProps<{
    title: string
    description: string
    confirmLabel?: string
    destructive?: boolean
    loading?: boolean
    /** When set, the user must type this text before the button enables. */
    typeToConfirm?: string
  }>(),
  { confirmLabel: 'Confirm', destructive: true },
)
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ confirm: [] }>()

const typed = ref('')
watch(open, (o) => {
  if (o) typed.value = ''
})
const ready = computed(() => !props.typeToConfirm || typed.value.trim() === props.typeToConfirm)
</script>

<template>
  <AlertDialog v-model:open="open">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>{{ title }}</AlertDialogTitle>
        <AlertDialogDescription>{{ description }}</AlertDialogDescription>
      </AlertDialogHeader>
      <slot />
      <div v-if="typeToConfirm" class="grid gap-2">
        <p class="text-sm">
          Type <span class="font-mono font-semibold">{{ typeToConfirm }}</span> to confirm.
        </p>
        <Input v-model="typed" autocomplete="off" autocapitalize="off" spellcheck="false" :placeholder="typeToConfirm" />
      </div>
      <AlertDialogFooter>
        <AlertDialogCancel :disabled="loading">Cancel</AlertDialogCancel>
        <Button :variant="destructive ? 'destructive' : 'default'" :disabled="!ready || loading" @click="emit('confirm')">
          {{ loading ? 'Working...' : confirmLabel }}
        </Button>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
