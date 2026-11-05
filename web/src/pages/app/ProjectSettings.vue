<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { Button } from '@/components/ui/button'
import FormCard from '@/components/FormCard.vue'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { keys, useProject, useProjectEnv, useProjects } from '@/composables/queries'
import { ApiError, api } from '@/lib/api'
import { appRoute } from '@/lib/appLinks'
import { friendlyError } from '@/lib/errors'
import { toastOk } from '@/lib/notify'
import { aliasOf, dependentsFromError, projectIdOf, projectLinks, referenceText, usedBy } from '@/lib/projects'
import { listWords } from '@/lib/services'
import type { App } from '@/lib/types'

// Which project the app is in, and its short name there: the hostname the
// other apps use and the name in ${{name.VAR}}.
const props = defineProps<{ app: App }>()
const router = useRouter()
const qc = useQueryClient()
const projects = useProjects()
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

// ---------------------------------------------------------------- move
const target = ref('')
const others = computed(() => (projects.data.value?.projects ?? []).filter((p) => p.id !== projectIdOf(props.app)))
const move = useMutation({
  mutationFn: () => api.updateApp(props.app.slug, { project: target.value }),
  onSuccess: async (a) => {
    qc.setQueryData(keys.app(a.slug), a)
    void qc.invalidateQueries({ queryKey: keys.apps })
    toastOk(`Moved ${a.name} to ${a.project?.name ?? 'the other project'}`)
    await router.replace(appRoute(a, 'app-settings'))
  },
})
const moveFail = computed(() => {
  const e = move.error.value
  if (!e) return ''
  const msg = friendlyError(e).message
  // A refused move names the apps that read this one's variables.
  const deps = e instanceof ApiError && e.code === 'app_referenced' ? dependentsFromError(e.details) : []
  return deps.length ? `${msg} ${listWords(deps.map((d) => `${d.alias} (${d.vars.join(', ') || 'its variables'})`))} ${deps.length === 1 ? 'reads' : 'read'} its variables.` : msg
})
</script>

<template>
  <div class="grid gap-4">
    <form novalidate @submit.prevent="!aliasError && saveAlias.mutate()">
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
        <template #hint>Lowercase letters, numbers and dashes.</template>
        <template #actions>
          <Button type="submit" :disabled="!!aliasError || alias.trim() === aliasOf(app) || saveAlias.isPending.value">{{ saveAlias.isPending.value ? 'Saving...' : 'Save' }}</Button>
        </template>
      </FormCard>
    </form>

    <form v-if="others.length" novalidate @submit.prevent="target && move.mutate()">
      <FormCard title="Move to another project" description="The app keeps its address and saved files. After the move it cannot reach the apps of this project by their short name.">
        <div class="grid gap-2">
          <Select v-model="target">
            <SelectTrigger class="w-64 max-w-full" aria-label="Project to move it to"><SelectValue placeholder="Pick a project" /></SelectTrigger>
            <SelectContent>
              <SelectItem v-for="p in others" :key="p.id" :value="p.id">{{ p.name }}</SelectItem>
            </SelectContent>
          </Select>
          <p v-if="moveFail" class="text-[0.8rem] text-destructive">{{ moveFail }}</p>
        </div>
        <template #actions>
          <Button type="submit" variant="outline" :disabled="!target || move.isPending.value">{{ move.isPending.value ? 'Moving...' : 'Move' }}</Button>
        </template>
      </FormCard>
    </form>
  </div>
</template>
