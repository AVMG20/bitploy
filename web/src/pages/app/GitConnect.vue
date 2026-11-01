<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import RepoPicker from './RepoPicker.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import type { App, GitSourceInput } from '@/lib/types'

const props = defineProps<{ app: App }>()
const qc = useQueryClient()
const slug = computed(() => props.app.slug)

const github = useQuery({ queryKey: keys.github, queryFn: api.github })
const canPick = computed(() => (github.data.value?.installations ?? []).some((i) => i.status === 'active'))

// Pick from the user's GitHub repositories, or type an address (other hosts, public repositories).
const chosenTab = ref<'github' | 'url' | null>(null)
const tab = computed({
  get: () => chosenTab.value ?? (canPick.value ? 'github' : 'url'),
  set: (v) => (chosenTab.value = v),
})

// What RepoPicker has picked, as the git-source call wants it.
const picked = ref<GitSourceInput | null>(null)

const link = useMutation({
  mutationFn: (input: GitSourceInput) => api.setGitSource(slug.value, input),
  onSuccess: (updated) => {
    qc.setQueryData(keys.app(slug.value), updated)
    void qc.invalidateQueries({ queryKey: keys.apps })
    const g = updated.git_source
    toastOk('Repository connected', g?.push_ready ? `Every push to ${g.branch} deploys the app. Press Redeploy to build it now.` : 'Press Redeploy to build it now.')
  },
  onError: (e) => toastError(e),
})
function connectPicked() {
  if (picked.value) link.mutate(picked.value)
}

const urlForm = useForm({
  validationSchema: toTypedSchema(
    z.object({
      repo_url: z.string().trim().min(1, 'Enter the address of the repository.').regex(/^https:\/\//i, 'The address must start with https://'),
      branch: z.string().trim().max(200, 'That name is too long.'),
      subdir: z.string().trim().max(200, 'That path is too long.'),
    }),
  ),
  initialValues: { repo_url: '', branch: '', subdir: '' },
})
const urlPlaceholder = 'https://github.com/owner/repo'
const urlAuto = ref(true)
const urlIsGitHub = computed(() => /^https:\/\/(www\.)?github\.com\//i.test(urlForm.values.repo_url ?? ''))
const submitUrl = urlForm.handleSubmit((v) =>
  link.mutate({ repo_url: v.repo_url, branch: v.branch || undefined, subdir: v.subdir || undefined, auto_deploy: urlIsGitHub.value ? urlAuto.value : false }),
)
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Connect a repository</CardTitle>
      <CardDescription>Build this app from git instead of uploaded files. Every push can deploy it by itself.</CardDescription>
    </CardHeader>
    <CardContent class="grid gap-4">
      <Tabs v-model="tab" class="min-w-0">
        <TabsList>
          <TabsTrigger value="github">From GitHub</TabsTrigger>
          <TabsTrigger value="url">From an address</TabsTrigger>
        </TabsList>
      </Tabs>

      <template v-if="tab === 'github'">
        <RepoPicker v-model="picked" />
        <div v-if="picked">
          <Button :disabled="link.isPending.value" @click="connectPicked">{{ link.isPending.value ? 'Checking...' : 'Connect repository' }}</Button>
        </div>
      </template>

      <form v-else class="grid gap-4" novalidate @submit.prevent="submitUrl">
        <FormField v-slot="{ componentField }" name="repo_url">
          <FormItem>
            <FormLabel>Repository address</FormLabel>
            <FormControl><Input :placeholder="urlPlaceholder" autocapitalize="off" autocomplete="off" spellcheck="false" class="font-mono" v-bind="componentField" /></FormControl>
            <FormDescription>Public repositories on any host work as they are. Private ones need GitHub connected first.</FormDescription>
            <FormMessage />
          </FormItem>
        </FormField>
        <div class="grid gap-4 sm:grid-cols-2 sm:items-start">
          <FormField v-slot="{ componentField }" name="branch">
            <FormItem>
              <FormLabel>Branch (optional)</FormLabel>
              <FormControl><Input placeholder="Default branch" autocapitalize="off" autocomplete="off" spellcheck="false" class="font-mono" v-bind="componentField" /></FormControl>
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
        <div v-if="urlIsGitHub" class="flex items-center justify-between gap-4 text-sm">
          <Label for="url-auto" class="font-medium">Deploy on every push</Label>
          <Switch id="url-auto" v-model="urlAuto" />
        </div>
        <div>
          <Button type="submit" :disabled="link.isPending.value">{{ link.isPending.value ? 'Checking...' : 'Connect repository' }}</Button>
        </div>
      </form>
    </CardContent>
    <CardFooter>
      <p class="text-xs text-muted-foreground">Once connected, the app is built from this repository. To send files again, disconnect it.</p>
    </CardFooter>
  </Card>
</template>
