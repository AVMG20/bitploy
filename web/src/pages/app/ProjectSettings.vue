<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import FormCard from '@/components/FormCard.vue'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { keys, useProject, useProjectEnv } from '@/composables/queries'
import { useSaveBarEntry } from '@/composables/useSaveBar'
import { api } from '@/lib/api'
import { friendlyError } from '@/lib/errors'
import { toastOk } from '@/lib/notify'
import { aliasOf, projectIdOf, projectLinks, referenceText, usedBy } from '@/lib/projects'
import { listWords } from '@/lib/services'
import type { App } from '@/lib/types'

// Which project the app is in, and its short name there: the hostname the
// other apps use and the name in ${{name.VAR}}.
const props = defineProps<{ app: App }>()
const qc = useQueryClient()
const { project } = useProject(() => projectIdOf(props.app))
const siblings = computed(() => project.value?.apps ?? [])
const env = useProjectEnv(siblings)
const users = computed(() => usedBy(props.app, siblings.value, projectLinks(siblings.value, env.value)))

// ---------------------------------------------------------------- short name
const alias = ref(aliasOf(props.app))
watch(() => aliasOf(props.app), (a) => (alias.value = a))
const aliasError = computed(() => {
  const a = alias.value.trim()
  if (!/^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$/.test(a)) return 'Use 1 to 32 lowercase letters, numbers and dashes, like api or db-2.'
  if (siblings.value.some((s) => s.id !== props.app.id && aliasOf(s) === a)) return `Another app of ${project.value?.name} is called ${a}.`
  return ''
})
const saveAlias = useMutation({
  mutationFn: () => api.updateApp(props.app.slug, { alias: alias.value.trim() }),
  onSuccess: (a) => {
    qc.setQueryData(keys.app(a.slug), a)
    void qc.invalidateQueries({ queryKey: keys.apps })
    toastOk(`Short name is now ${aliasOf(a)}`, users.value.length ? `Change ${listWords(users.value.map((u) => u.name))} to use it.` : undefined)
  },
})
const aliasFail = computed(() => (saveAlias.error.value ? friendlyError(saveAlias.error.value).message : ''))

useSaveBarEntry({
  changes: () => Number(alias.value.trim() !== aliasOf(props.app)),
  save: async () => {
    if (aliasError.value) return false
    try {
      await saveAlias.mutateAsync()
      return true
    } catch {
      return false
    }
  },
  discard: () => {
    alias.value = aliasOf(props.app)
    saveAlias.reset()
  },
})
</script>

<template>
  <div class="grid gap-4">
    <form novalidate @submit.prevent>
      <FormCard title="Short name">
        <template #description>
          The other apps of {{ project?.name ?? 'the project' }} reach this one at this name, and use its variables as
          <span class="font-mono">{{ referenceText(aliasOf(app), 'NAME') }}</span>.
        </template>
        <div class="grid gap-2">
          <Label for="app-alias" class="sr-only">Short name</Label>
          <Input id="app-alias" v-model="alias" class="max-w-xs font-mono" autocapitalize="off" autocomplete="off" spellcheck="false" :aria-invalid="!!aliasError || undefined" />
          <p v-if="aliasError" class="text-[0.8rem] text-destructive">{{ aliasError }}</p>
          <p v-else-if="aliasFail" class="text-[0.8rem] text-destructive">{{ aliasFail }}</p>
          <p v-else-if="users.length && alias.trim() !== aliasOf(app)" class="text-[0.8rem] text-warning">
            {{ listWords(users.map((u) => u.name)) }} {{ users.length === 1 ? 'uses' : 'use' }} <span class="font-mono">{{ aliasOf(app) }}</span> in {{ users.length === 1 ? 'its' : 'their' }} variables. Change those too, or they lose the connection.
          </p>
        </div>
      </FormCard>
    </form>
  </div>
</template>
