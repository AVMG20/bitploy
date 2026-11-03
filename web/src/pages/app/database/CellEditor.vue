<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { affinityOf, effectiveAffinity, guessKind, inputText, kindFromValue, parseInput } from '@/lib/dbvalue'
import { toastError } from '@/lib/notify'
import type { DbColumn, DbValue } from '@/lib/types'

// Edits one cell where it is. Enter saves, Esc cancels, leaving the cell saves
// when the value changed, Tab saves and moves on. Long or multi-line text gets
// a textarea that floats over the cell: there Enter adds a line and Ctrl/Cmd+Enter saves.
const props = defineProps<{ column: DbColumn; value: DbValue }>()
const emit = defineEmits<{ save: [value: DbValue]; cancel: []; next: [back: boolean] }>()

const aff = affinityOf(props.column.type)
const multiline = typeof props.value === 'string' && (props.value.includes('\n') || props.value.length > 80)
const text = ref(inputText(props.value))
const error = ref('')
const field = ref<HTMLInputElement | HTMLTextAreaElement | null>(null)
// Once it has said save or cancel, the blur that follows when it is removed means nothing.
let done = false

onMounted(() => {
  const el = field.value
  if (!el) return
  el.focus()
  el.setSelectionRange(el.value.length, el.value.length)
})

function cancel() {
  done = true
  emit('cancel')
}

/** Save what was typed. False when it is not a valid value (the editor stays open and says why). */
function commit(): boolean {
  if (text.value === inputText(props.value)) {
    cancel()
    return true
  }
  // A number stays a number and text stays text, in a column without a type too.
  const kind = props.value === null ? guessKind(text.value) : kindFromValue(props.value)
  const p = parseInput(effectiveAffinity(aff, kind), text.value)
  if (!p.ok) {
    error.value = p.error
    return false
  }
  done = true
  emit('save', p.value)
  return true
}

function setNull() {
  if (props.value === null) cancel()
  else {
    done = true
    emit('save', null)
  }
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault()
    e.stopPropagation()
    cancel()
  } else if (e.key === 'Enter' && (!multiline || e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    commit()
  } else if (e.key === 'Tab') {
    e.preventDefault()
    if (commit()) emit('next', e.shiftKey)
  }
}

function onBlur() {
  if (done) return
  if (!commit()) {
    toastError(new Error(error.value))
    cancel()
  }
}
</script>

<template>
  <div :class="multiline ? 'relative min-h-5' : 'flex min-w-40 items-center gap-1'" @keydown.esc.stop>
    <div v-if="multiline" class="absolute -top-1.5 -left-2 z-20 grid w-[min(28rem,75vw)] gap-1.5 rounded-lg border bg-popover p-1.5 shadow-lg ring-2 ring-ring/40">
      <textarea
        ref="field"
        v-model="text"
        rows="5"
        class="max-h-72 min-h-24 w-full resize-y overflow-auto rounded-md border bg-card px-2 py-1.5 font-mono text-[13px] outline-none focus-visible:border-ring"
        :aria-label="`Value of ${column.name}`"
        spellcheck="false"
        @keydown="onKey"
        @blur="onBlur"
      />
      <div class="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
        <span class="font-sans">Ctrl/Cmd+Enter saves &middot; Esc cancels</span>
        <button v-if="!column.notnull" type="button" class="rounded border px-1.5 py-0.5 font-sans font-medium hover:bg-muted hover:text-foreground" @mousedown.prevent @click="setNull">NULL</button>
      </div>
      <p v-if="error" class="font-sans text-[11px] font-medium text-destructive">{{ error }}</p>
    </div>
    <template v-else>
      <input
        ref="field"
        v-model="text"
        class="h-6 min-w-0 flex-1 rounded border bg-card px-1.5 font-mono text-[13px] outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/40"
        :class="error && 'border-destructive'"
        :aria-label="`Value of ${column.name}`"
        :aria-invalid="!!error"
        :title="error || undefined"
        :placeholder="value === null ? 'NULL' : ''"
        spellcheck="false"
        autocomplete="off"
        @keydown="onKey"
        @blur="onBlur"
      />
      <button v-if="!column.notnull" type="button" class="shrink-0 rounded border px-1 text-[11px] font-medium text-muted-foreground hover:bg-muted hover:text-foreground" title="Set to NULL" @mousedown.prevent @click="setNull">NULL</button>
    </template>
  </div>
</template>
