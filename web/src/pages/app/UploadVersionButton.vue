<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { Upload } from 'lucide-vue-next'
import BuildTypeFields from '@/components/BuildTypeFields.vue'
import SourceDropZone from '@/components/SourceDropZone.vue'
import type { UploadChoice } from '@/components/SourceDropZone.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { keys, useApp } from '@/composables/queries'
import { api } from '@/lib/api'
import { detectSource, outputDirError } from '@/lib/detect'
import { friendlyError } from '@/lib/errors'
import type { FriendlyError } from '@/lib/errors'
import { toastOk } from '@/lib/notify'
import { versionLabel } from '@/lib/format'
import type { BuildMode, Deployment } from '@/lib/types'

// "Upload new version": the same drop zone as the New app form, for an app
// whose files were uploaded. Apps that build from git are updated by pushing.
const props = withDefaults(defineProps<{ slug: string; label?: string; variant?: 'default' | 'outline' }>(), { label: 'Upload new version', variant: 'outline' })
const qc = useQueryClient()
const app = useApp(() => props.slug)

const open = ref(false)
const choice = ref<UploadChoice | null>(null)
const mode = ref<BuildMode>('auto')
const outputDir = ref('')
const homePage = ref('')
const busy = ref(false)
const sent = ref(0)
const checking = ref(false)
const error = ref<FriendlyError | null>(null)

const visible = computed(() => !!app.data.value && !app.data.value.git_source)
const first = computed(() => !app.data.value?.current_deployment)

// Start from the app's own setting each time the dialog opens.
watch(open, (o) => {
  if (!o) return
  choice.value = null
  error.value = null
  mode.value = app.data.value?.build_mode ?? 'auto'
  homePage.value = app.data.value?.home_page ?? ''
  outputDir.value = app.data.value?.output_dir ?? ''
})

const detected = computed(() =>
  choice.value?.paths ? detectSource(choice.value.paths, mode.value, homePage.value, 'Dockerfile', choice.value.packageJson, choice.value.nextConfig) : null,
)
const ready = computed(() => !!choice.value && detected.value?.kind !== 'none' && !outputDirError(outputDir.value))
const buttonLabel = computed(() => (checking.value ? 'Checking your files...' : busy.value ? `Sending... ${Math.round(sent.value * 100)}%` : 'Deploy'))

async function deploy() {
  if (!choice.value || !app.data.value) return
  busy.value = true
  checking.value = false
  sent.value = 0
  error.value = null
  const before = { build_mode: app.data.value.build_mode, home_page: app.data.value.home_page, output_dir: app.data.value.output_dir }
  // The home page is only saved when there are several pages to choose from.
  const home = detected.value?.kind === 'static' && detected.value.choices ? (detected.value.home ?? '') : before.home_page
  let changed = false
  try {
    // The deploy reads the app's build mode, output folder and home page, so
    // they are saved first, and put back when the upload is refused.
    const output = outputDir.value.trim()
    if (mode.value !== before.build_mode || home !== before.home_page || output !== before.output_dir) {
      await api.updateApp(props.slug, { build_mode: mode.value, home_page: home, output_dir: output })
      changed = true
    }
    let d: Deployment
    try {
      d = await api.uploadApp(
        props.slug,
        choice.value.zip,
        (f) => (sent.value = f),
        () => (checking.value = true),
      )
    } catch (e) {
      if (changed) await api.updateApp(props.slug, before).catch(() => {})
      throw e
    }
    toastOk(`Deploying ${versionLabel(d.seq).toLowerCase()}`, first.value ? 'Follow it on the Overview.' : 'Your app keeps running until the new version is ready.')
    open.value = false
    void qc.invalidateQueries({ queryKey: keys.deployments(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.apps })
  } catch (e) {
    error.value = friendlyError(e)
  } finally {
    busy.value = false
    checking.value = false
  }
}
</script>

<template>
  <template v-if="visible">
    <Button :variant="variant" @click="open = true"><Upload /> {{ label }}</Button>
    <Dialog v-model:open="open">
      <DialogContent class="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{{ first ? 'Upload your files' : 'Upload a new version' }}</DialogTitle>
          <DialogDescription v-if="first">Drop your project folder, a .zip or a single .html page. Bitploy builds it and puts it online.</DialogDescription>
          <DialogDescription v-else>
            Drop the folder again, with your changes. The version that runs now stays available, so you can go back to it.
          </DialogDescription>
        </DialogHeader>
        <SourceDropZone v-model="choice" v-model:mode="mode" v-model:home-page="homePage" :output-dir="outputDir" :disabled="busy" />
        <BuildTypeFields v-if="choice" v-model:mode="mode" v-model:output-dir="outputDir" :detected="detected" :later="!choice.paths" :disabled="busy" id-prefix="upload" />
        <Alert v-if="error" variant="destructive">
          <AlertTitle>{{ error.message }}</AlertTitle>
          <AlertDescription v-if="error.hint">{{ error.hint }}</AlertDescription>
        </Alert>
        <DialogFooter>
          <Button variant="ghost" :disabled="busy" @click="open = false">Cancel</Button>
          <Button :disabled="busy || !ready" @click="deploy">{{ buttonLabel }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </template>
</template>
