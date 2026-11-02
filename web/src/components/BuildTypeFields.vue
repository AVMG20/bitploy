<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { detectedType, outputDirError } from '@/lib/detect'
import type { Detected } from '@/lib/detect'
import type { BuildMode } from '@/lib/types'

// The "Type" of an app: how its files become a running app. Used by the New app
// form, the upload dialog and the app's Settings. `detected` is what bitploy
// makes of the files (null when it cannot know yet: a GitHub repository, or a
// zip this page could not read).
const props = withDefaults(defineProps<{ detected?: Detected | null; /** The files are not known here: the type is found when it builds. */ later?: boolean; disabled?: boolean; idPrefix?: string }>(), {
  detected: null,
  later: false,
  idPrefix: 'type',
})
const mode = defineModel<BuildMode>('mode', { default: 'auto' })
/** The output folder of a static build; empty means the default of the framework. */
const outputDir = defineModel<string>('outputDir', { default: '' })

const options: { value: BuildMode; label: string }[] = [
  { value: 'node', label: 'Node.js app' },
  { value: 'static_build', label: 'Static build (Vite, React, Vue, Astro)' },
  { value: 'static', label: 'Plain website (HTML files)' },
  { value: 'dockerfile', label: 'Own Dockerfile' },
]

const autoLabel = computed(() => {
  const t = detectedType(props.detected)
  return t ? `Auto-detect: ${t}` : 'Auto-detect'
})

const staticBuild = computed(() => {
  if (mode.value === 'static_build') return true
  const d = props.detected
  return mode.value === 'auto' && d?.kind === 'template' && d.type === 'static_build'
})
const detectedOutput = computed(() => (props.detected?.kind === 'template' ? (props.detected.output ?? '') : ''))
// Until the person types, the field shows the detected folder.
const touched = ref(false)
const shown = computed({
  get: () => (touched.value ? outputDir.value : outputDir.value || detectedOutput.value),
  set: (v: string) => {
    touched.value = true
    outputDir.value = v
  },
})
// New files: show what they need again, unless a folder was typed.
watch(
  () => props.detected,
  () => {
    if (!outputDir.value) touched.value = false
  },
)
const outputError = computed(() => outputDirError(outputDir.value))
</script>

<template>
  <div class="grid min-w-0 gap-3">
    <div class="grid gap-1.5">
      <Label :for="`${idPrefix}-type`">Type</Label>
      <Select v-model="mode" :disabled="disabled">
        <SelectTrigger :id="`${idPrefix}-type`" class="w-full sm:max-w-sm"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="auto">{{ autoLabel }}</SelectItem>
          <SelectItem v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</SelectItem>
        </SelectContent>
      </Select>
      <p v-if="mode === 'auto' && later && !detected" class="text-xs text-muted-foreground">Detected when it builds.</p>
    </div>
    <div v-if="staticBuild" class="grid gap-1.5">
      <Label :for="`${idPrefix}-output`">Output folder</Label>
      <Input
        :id="`${idPrefix}-output`"
        v-model="shown"
        class="font-mono sm:max-w-sm"
        :placeholder="detectedOutput || 'dist'"
        autocapitalize="off"
        autocomplete="off"
        spellcheck="false"
        :disabled="disabled"
        :aria-invalid="outputError ? true : undefined"
      />
      <p v-if="outputError" class="text-xs text-destructive" role="alert">{{ outputError }}</p>
      <p v-else class="text-xs text-muted-foreground">The folder the build writes the site to. Leave it as it is unless the build uses another one.</p>
    </div>
  </div>
</template>
