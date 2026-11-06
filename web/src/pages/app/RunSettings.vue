<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import FormCard from '@/components/FormCard.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { keys } from '@/composables/queries'
import { ApiError, api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import { runChanges, runErrors, runFieldOfError, runFieldsFor, runFormOf } from '@/lib/runSettings'
import type { RunField, RunForm } from '@/lib/runSettings'
import type { App } from '@/lib/types'

// How the app runs once it is built: web app or worker, its port and health
// page, and for an app with its own Dockerfile where that file is. Each field
// shows only where it applies; all of them apply on the next deploy.
const props = defineProps<{ app: App }>()
const qc = useQueryClient()

const form = ref<RunForm>(runFormOf(props.app))
let loadedFor = ''
watch(
  () => props.app,
  (a) => {
    // Once per app, so a poll does not wipe unsaved edits.
    if (loadedFor !== a.id) {
      loadedFor = a.id
      form.value = runFormOf(a)
    }
  },
  { immediate: true },
)

const shown = computed(() => runFieldsFor(props.app, form.value.type))
const anyShown = computed(() => Object.values(shown.value).some(Boolean))
const changes = computed(() => runChanges(props.app, form.value, shown.value))
const changed = computed(() => Object.keys(changes.value).length > 0)

// Problems found here, then the server's answer for the field it is about.
const serverErrors = ref<Partial<Record<RunField, string>>>({})
const tried = ref(false)
const errors = computed(() => ({ ...(tried.value ? runErrors(form.value, shown.value) : {}), ...serverErrors.value }))
watch(form, () => (serverErrors.value = {}), { deep: true })

const save = useMutation({
  mutationFn: () => api.updateApp(props.app.slug, changes.value),
  onSuccess: (updated) => {
    toastOk('Saved', 'It applies on the next deploy.')
    form.value = runFormOf(updated)
    tried.value = false
    qc.setQueryData(keys.app(props.app.slug), updated)
    void qc.invalidateQueries({ queryKey: keys.apps })
  },
  onError: (e) => {
    const field = e instanceof ApiError ? runFieldOfError(e.code) : null
    if (field && e instanceof ApiError) serverErrors.value = { [field]: [e.message, e.hint].filter(Boolean).join(' ') }
    else toastError(e)
  },
})
function submit() {
  tried.value = true
  if (Object.keys(runErrors(form.value, shown.value)).length) return
  save.mutate()
}
</script>

<template>
  <form v-if="anyShown" id="run" class="scroll-mt-16" novalidate @submit.prevent="submit">
    <FormCard title="How it runs" description="Settings for the next deploy. Leave a field as it is unless the app needs it.">
      <div class="grid gap-4 sm:grid-cols-2 sm:items-start">
        <div v-if="shown.type" class="grid gap-1.5 sm:col-span-2">
          <Label for="run-type">Kind of app</Label>
          <Select v-model="form.type">
            <SelectTrigger id="run-type" class="w-full sm:max-w-sm" :aria-invalid="errors.type ? true : undefined"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="web">Website or API, with a web address</SelectItem>
              <SelectItem value="worker">Worker, like a bot (no web address)</SelectItem>
            </SelectContent>
          </Select>
          <p v-if="errors.type" class="text-xs text-destructive" role="alert">{{ errors.type }}</p>
          <p v-else class="text-xs text-muted-foreground">
            {{ form.type === 'web' ? 'Visitors open it at its address, and bitploy checks that it answers.' : 'It gets no address and runs in the background. It counts as working when it is still running after 30 seconds.' }}
          </p>
        </div>

        <div v-if="shown.port" class="grid gap-1.5">
          <Label for="run-port">Port</Label>
          <Input
            id="run-port"
            v-model="form.port"
            inputmode="numeric"
            placeholder="From the Dockerfile"
            autocomplete="off"
            class="font-mono"
            :aria-invalid="errors.port ? true : undefined"
          />
          <p v-if="errors.port" class="text-xs text-destructive" role="alert">{{ errors.port }}</p>
          <p v-else class="text-xs text-muted-foreground">The port your app listens on, like 3000. Leave it empty to use the EXPOSE line of the Dockerfile.</p>
        </div>

        <div v-if="shown.health_path" class="grid gap-1.5">
          <Label for="run-health">Health check page</Label>
          <Input
            id="run-health"
            v-model="form.health_path"
            placeholder="/"
            autocapitalize="off"
            autocomplete="off"
            spellcheck="false"
            class="font-mono"
            :aria-invalid="errors.health_path ? true : undefined"
          />
          <p v-if="errors.health_path" class="text-xs text-destructive" role="alert">{{ errors.health_path }}</p>
          <p v-else class="text-xs text-muted-foreground">The page bitploy opens after a deploy to see that the app works, like /healthz. Any answer except a server error counts.</p>
        </div>

        <div v-if="shown.dockerfile_path" class="grid gap-1.5">
          <Label for="run-dockerfile">Dockerfile</Label>
          <Input
            id="run-dockerfile"
            v-model="form.dockerfile_path"
            placeholder="Dockerfile"
            autocapitalize="off"
            autocomplete="off"
            spellcheck="false"
            class="font-mono"
            :aria-invalid="errors.dockerfile_path ? true : undefined"
          />
          <p v-if="errors.dockerfile_path" class="text-xs text-destructive" role="alert">{{ errors.dockerfile_path }}</p>
          <p v-else class="text-xs text-muted-foreground">Where the Dockerfile is in your code, like docker/Dockerfile.prod.</p>
        </div>

        <div v-if="shown.context_dir" class="grid gap-1.5">
          <Label for="run-context">Build folder</Label>
          <Input
            id="run-context"
            v-model="form.context_dir"
            placeholder="."
            autocapitalize="off"
            autocomplete="off"
            spellcheck="false"
            class="font-mono"
            :aria-invalid="errors.context_dir ? true : undefined"
          />
          <p v-if="errors.context_dir" class="text-xs text-destructive" role="alert">{{ errors.context_dir }}</p>
          <p v-else class="text-xs text-muted-foreground">The folder the Dockerfile copies files from. A dot is the top folder of your code; use backend for a folder of that name.</p>
        </div>
      </div>
      <template #hint>Applies on the next deploy.</template>
      <template #actions>
        <Button type="submit" :disabled="!changed || save.isPending.value">{{ save.isPending.value ? 'Saving...' : 'Save' }}</Button>
      </template>
    </FormCard>
  </form>
</template>
