<script setup lang="ts">
import { ref } from 'vue'
import { Search, X } from 'lucide-vue-next'
import { Kbd } from '@/components/ui/kbd'
import { useSlashFocus } from '@/composables/useShortcuts'
import { cn } from '@/lib/utils'

// The search field above a list. "/" jumps into it, Escape clears it, Enter
// emits `enter` (for example to open the only match).
const props = withDefaults(defineProps<{ placeholder?: string; label?: string; slash?: boolean; class?: string }>(), {
  placeholder: 'Search',
  label: 'Search',
  slash: true,
  class: '',
})
const model = defineModel<string>({ required: true })
const emit = defineEmits<{ enter: [] }>()
const input = ref<HTMLInputElement | null>(null)

function focus() {
  input.value?.focus()
  input.value?.select()
}
if (props.slash) useSlashFocus(focus)

function onEscape() {
  if (model.value) model.value = ''
  else input.value?.blur()
}
defineExpose({ focus })
</script>

<template>
  <div :class="cn('relative min-w-0', props.class)">
    <Search class="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
    <input
      ref="input"
      v-model="model"
      type="search"
      :placeholder="placeholder"
      :aria-label="label"
      :aria-keyshortcuts="slash ? '/' : undefined"
      autocomplete="off"
      spellcheck="false"
      data-slot="search-input"
      class="h-8 w-full min-w-0 rounded-lg border border-input bg-card pr-9 pl-8.5 text-base transition-colors outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm dark:bg-input/30 [&::-webkit-search-cancel-button]:hidden"
      @keydown.esc.prevent="onEscape"
      @keydown.enter.prevent="emit('enter')"
    />
    <button
      v-if="model"
      type="button"
      class="absolute top-1/2 right-1 flex size-6 -translate-y-1/2 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
      aria-label="Clear search"
      @click="model = ''; input?.focus()"
    >
      <X class="size-3.5" />
    </button>
    <Kbd v-else-if="slash" class="absolute top-1/2 right-2 hidden -translate-y-1/2 sm:inline-flex">/</Kbd>
  </div>
</template>
