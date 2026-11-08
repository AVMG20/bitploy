<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import DangerRow from '@/components/DangerRow.vue'
import DangerZone from '@/components/DangerZone.vue'
import FormCard from '@/components/FormCard.vue'
import SettingsSection from '@/components/SettingsSection.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { keys } from '@/composables/queries'
import { useProjectData } from '@/composables/useProjectData'
import { api } from '@/lib/api'
import { friendlyError } from '@/lib/errors'
import { toastError, toastOk } from '@/lib/notify'
import { ownerName } from '@/lib/projects'

// The project's name, and deleting it with everything in it. Both are up to
// its owner: someone it is shared with sees the name and can leave.
const props = defineProps<{ projectId: string }>()
const router = useRouter()
const qc = useQueryClient()
const { project, apps } = useProjectData(() => props.projectId)
const isOwner = computed(() => !project.value?.role || project.value.role === 'owner')

const name = ref('')
watch(
  () => project.value?.name,
  (n) => (name.value = n ?? ''),
  { immediate: true },
)
const rename = useMutation({
  mutationFn: () => api.updateProject(props.projectId, { name: name.value.trim() }),
  onSuccess: () => {
    toastOk('Project renamed')
    void qc.invalidateQueries({ queryKey: keys.projects })
  },
})
const renameError = computed(() => (rename.error.value ? friendlyError(rename.error.value).message : ''))
const unchanged = computed(() => name.value.trim() === (project.value?.name ?? ''))

const deleteOpen = ref(false)
const remove = useMutation({
  mutationFn: () => api.deleteProject(props.projectId, project.value?.name ?? '', apps.value.length > 0),
  onSuccess: () => {
    toastOk(`Deleted ${project.value?.name ?? 'the project'}`)
    deleteOpen.value = false
    void qc.invalidateQueries({ queryKey: keys.apps })
    void router.push('/')
  },
  onError: (e) => toastError(e),
})
const leaveOpen = ref(false)
const leave = useMutation({
  mutationFn: () => api.removeProjectMember(props.projectId, 'me'),
  onSuccess: () => {
    toastOk(`You left ${project.value?.name ?? 'the project'}`)
    leaveOpen.value = false
    void qc.invalidateQueries({ queryKey: keys.apps })
    void router.push('/')
  },
  onError: (e) => toastError(e),
})
const deleteText = computed(() => {
  const n = apps.value.length
  if (!n) return 'The project is empty, so nothing else is deleted.'
  const names = apps.value.map((a) => a.name).join(', ')
  return `This deletes ${n === 1 ? 'its app' : `its ${n} apps`} (${names}) with their saved files, databases, domains and backups. This cannot be undone. Download a backup first (each app's Storage tab) if you may need the data.`
})
</script>

<template>
  <div v-if="project" class="grid max-w-6xl gap-10">
    <SettingsSection title="General">
      <form novalidate @submit.prevent="name.trim() && !unchanged && rename.mutate()">
        <FormCard title="Project name">
          <div class="grid gap-2">
            <Label for="project-name" class="sr-only">Project name</Label>
            <Input id="project-name" v-model="name" class="max-w-sm" autocomplete="off" :disabled="!isOwner" :aria-invalid="!!renameError || undefined" />
            <p v-if="renameError" class="text-[0.8rem] text-destructive">{{ renameError }}</p>
            <p v-if="!isOwner" class="text-sm text-muted-foreground">Only {{ ownerName(project) }}, who owns the project, can rename it.</p>
          </div>
          <template #hint>Up to 80 characters.</template>
          <template v-if="isOwner" #actions>
            <Button type="submit" :disabled="!name.trim() || unchanged || rename.isPending.value">{{ rename.isPending.value ? 'Saving...' : 'Save name' }}</Button>
          </template>
        </FormCard>
      </form>
    </SettingsSection>

    <SettingsSection v-if="!isOwner && project.role === 'member'" id="danger" title="Danger zone">
      <DangerZone>
        <DangerRow title="Leave project" :description="`You lose access to ${project.name} and its apps. The apps stay as they are; to come back you need a new invite from ${ownerName(project)}.`">
          <Button variant="destructive" @click="leaveOpen = true">Leave project</Button>
        </DangerRow>
      </DangerZone>
    </SettingsSection>

    <SettingsSection v-if="isOwner" id="danger" title="Danger zone">
      <DangerZone>
        <DangerRow title="Delete project" :description="apps.length ? `Deletes ${project.name} and its ${apps.length === 1 ? 'app' : `${apps.length} apps`}, with their saved files, databases, domains and backups. Download a backup first if you may need the data.` : `Deletes ${project.name}.`">
          <Button variant="destructive" @click="deleteOpen = true">Delete project</Button>
        </DangerRow>
      </DangerZone>
    </SettingsSection>

    <ConfirmDialog
      v-model:open="deleteOpen"
      :title="`Delete ${project.name}?`"
      :description="deleteText"
      confirm-label="Delete project"
      :type-to-confirm="apps.length ? project.name : undefined"
      :loading="remove.isPending.value"
      @confirm="remove.mutate()"
    />
    <ConfirmDialog
      v-model:open="leaveOpen"
      :title="`Leave ${project.name}?`"
      :description="`You lose access to ${project.name} and its apps straight away. To come back you need a new invite from ${ownerName(project)}.`"
      confirm-label="Leave project"
      :loading="leave.isPending.value"
      @confirm="leave.mutate()"
    />
  </div>
</template>
