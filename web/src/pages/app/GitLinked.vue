<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { ExternalLink, Github, GitBranch } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { keys } from '@/composables/queries'
import { useSaveBarEntry } from '@/composables/useSaveBar'
import { useSession } from '@/composables/useSession'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import type { App, GitSource } from '@/lib/types'

const props = defineProps<{ app: App; source: GitSource }>()
const qc = useQueryClient()
const { isAdmin } = useSession()
const slug = computed(() => props.app.slug)

// The branch list comes from GitHub through the owner's connection. Without it
// (other hosts, no access) the branch is typed by hand.
const branches = useQuery({
  queryKey: computed(() => keys.githubBranches(props.source.repo ?? '')),
  queryFn: () => api.githubBranches(props.source.repo ?? ''),
  enabled: computed(() => !!props.source.repo),
  retry: false,
  staleTime: 60_000,
})
const branchList = computed(() => {
  const list = branches.data.value?.branches ?? []
  if (!list.length) return []
  return list.includes(props.source.branch) ? list : [props.source.branch, ...list]
})

const form = useForm({
  validationSchema: toTypedSchema(
    z.object({
      branch: z.string().trim().min(1, 'Enter the branch to deploy.').max(200, 'That name is too long.'),
      subdir: z.string().trim().max(200, 'That path is too long.'),
    }),
  ),
  initialValues: { branch: props.source.branch, subdir: props.source.subdir ?? '' },
})
let loadedFor = ''
watch(
  () => `${props.app.id}|${props.source.repo_url}|${props.source.branch}|${props.source.subdir ?? ''}`,
  (key) => {
    if (key !== loadedFor) {
      loadedFor = key
      form.resetForm({ values: { branch: props.source.branch, subdir: props.source.subdir ?? '' } })
    }
  },
  { immediate: true },
)

function refresh(updated: App) {
  qc.setQueryData(keys.app(slug.value), updated)
  void qc.invalidateQueries({ queryKey: keys.apps })
}

const save = useMutation({
  mutationFn: (v: { branch: string; subdir: string }) =>
    api.setGitSource(slug.value, { repo_url: props.source.repo_url, branch: v.branch, subdir: v.subdir, auto_deploy: props.source.auto_deploy }),
  onSuccess: (updated) => {
    refresh(updated)
    toastOk('Repository settings saved', `The next deploy builds ${updated.git_source?.branch ?? 'this branch'}.`)
  },
  onError: (e) => toastError(e),
})

useSaveBarEntry({
  changes: () => Number((form.values.branch ?? '').trim() !== props.source.branch) + Number((form.values.subdir ?? '').trim() !== (props.source.subdir ?? '')),
  later: true,
  save: async () => {
    const { valid } = await form.validate()
    if (!valid) return false
    try {
      await save.mutateAsync({ branch: (form.values.branch ?? '').trim(), subdir: (form.values.subdir ?? '').trim() })
      return true
    } catch {
      return false
    }
  },
  discard: () => form.resetForm({ values: { branch: props.source.branch, subdir: props.source.subdir ?? '' } }),
})

const toggle = useMutation({
  mutationFn: (on: boolean) => api.setAutoDeploy(slug.value, on),
  onSuccess: (updated) => {
    refresh(updated)
    toastOk(updated.git_source?.auto_deploy ? 'Auto-deploy is on' : 'Auto-deploy is off')
  },
  onError: (e) => toastError(e),
})

const disconnectOpen = ref(false)
const disconnect = useMutation({
  mutationFn: () => api.clearGitSource(slug.value),
  onSuccess: (updated) => {
    disconnectOpen.value = false
    refresh(updated)
    toastOk('Repository disconnected', 'You can upload files again.')
  },
  onError: (e) => toastError(e),
})

const onGitHub = computed(() => !!props.source.repo)
const repoName = computed(() => props.source.repo ?? props.source.repo_url.replace(/^https?:\/\//, '').replace(/\.git$/, ''))
// When pushes cannot arrive, the status sentence links to where it is fixed.
const fixTo = computed(() => {
  switch (props.source.push_status) {
    case 'not_connected':
    case 'no_access':
    case 'reconnect':
      return { name: 'settings-github' }
    case 'app_missing':
    case 'no_webhook':
      return isAdmin.value ? { name: 'admin', query: { tab: 'github' } } : null
    default:
      return null
  }
})
const statusText = computed(() => props.source.push_message.replace(/\.$/, ''))
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Source</CardTitle>
      <CardDescription>
        {{ onGitHub ? 'This app is built from a GitHub repository, not from uploaded files.' : 'This app is built from a git repository, not from uploaded files.' }}
      </CardDescription>
    </CardHeader>
    <CardContent class="grid gap-4">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
        <div class="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-muted/50"><Github class="size-4" /></div>
        <div class="min-w-0 text-sm">
          <a v-if="source.repo_html_url" :href="source.repo_html_url" target="_blank" rel="noopener noreferrer" class="inline-flex max-w-full items-center gap-1 font-medium hover:underline">
            <span class="truncate">{{ repoName }}</span> <ExternalLink class="size-3.5 shrink-0" />
          </a>
          <p v-else class="truncate font-medium">{{ repoName }}</p>
          <p class="flex items-center gap-1 text-xs text-muted-foreground">
            <GitBranch class="size-3" /> {{ source.branch }}<template v-if="source.subdir"> · folder {{ source.subdir }}</template>
          </p>
        </div>
        <Button class="ml-auto" size="sm" variant="outline" @click="disconnectOpen = true">Disconnect repo</Button>
      </div>

      <form class="grid gap-4" novalidate @submit.prevent>
        <div class="grid gap-4 sm:grid-cols-2 sm:items-start">
          <FormField v-slot="{ componentField }" name="branch">
            <FormItem>
              <FormLabel>Branch</FormLabel>
              <Select v-if="branchList.length" v-bind="componentField">
                <FormControl><SelectTrigger class="w-full"><SelectValue /></SelectTrigger></FormControl>
                <SelectContent>
                  <SelectItem v-for="b in branchList" :key="b" :value="b">{{ b }}</SelectItem>
                </SelectContent>
              </Select>
              <FormControl v-else><Input autocapitalize="off" autocomplete="off" spellcheck="false" class="font-mono" v-bind="componentField" /></FormControl>
              <FormMessage />
            </FormItem>
          </FormField>
          <FormField v-slot="{ componentField }" name="subdir">
            <FormItem>
              <FormLabel>Folder (optional)</FormLabel>
              <FormControl><Input placeholder="Whole repository" autocapitalize="off" autocomplete="off" spellcheck="false" class="font-mono" v-bind="componentField" /></FormControl>
              <FormMessage />
            </FormItem>
          </FormField>
        </div>
      </form>

      <Separator />

      <div class="flex items-start justify-between gap-4">
        <div class="min-w-0 text-sm">
          <p class="font-medium">Deploy on every push</p>
          <p class="mt-0.5 text-muted-foreground">
            <RouterLink v-if="fixTo" :to="fixTo" class="font-medium text-foreground underline-offset-2 hover:underline">{{ statusText }}</RouterLink>
            <template v-else>{{ statusText }}</template>
            <template v-if="source.last_push_at"> · last push <RelativeTime :date="source.last_push_at" /></template>
          </p>
        </div>
        <Switch
          :model-value="source.auto_deploy"
          :disabled="toggle.isPending.value || source.push_status === 'other_host'"
          aria-label="Deploy on every push"
          @update:model-value="(v) => toggle.mutate(v === true)"
        />
      </div>
    </CardContent>

    <ConfirmDialog
      v-model:open="disconnectOpen"
      title="Disconnect this repository?"
      description="Pushes stop deploying it, and you can upload files again."
      confirm-label="Disconnect"
      :loading="disconnect.isPending.value"
      @confirm="disconnect.mutate()"
    />
  </Card>
</template>
