<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { refDebounced } from '@vueuse/core'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Github, Lock, Search } from 'lucide-vue-next'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { keys } from '@/composables/queries'
import { useSession } from '@/composables/useSession'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import type { App, GitHubRepo, GitSourceInput } from '@/lib/types'

const props = defineProps<{ app: App }>()
const qc = useQueryClient()
const { isAdmin } = useSession()
const slug = computed(() => props.app.slug)

const github = useQuery({ queryKey: keys.github, queryFn: api.github })
const configured = computed(() => github.data.value?.app.configured ?? false)
const canPick = computed(() => (github.data.value?.installations ?? []).some((i) => i.status === 'active'))

// Pick from the user's GitHub repositories, or type an address (other hosts, public repositories).
const chosenTab = ref<'github' | 'url' | null>(null)
const tab = computed({
  get: () => chosenTab.value ?? (canPick.value ? 'github' : 'url'),
  set: (v) => (chosenTab.value = v),
})

const search = ref('')
const debounced = refDebounced(search, 250)
const repos = useQuery({
  queryKey: computed(() => keys.githubRepos(debounced.value.trim())),
  queryFn: () => api.githubRepos(debounced.value.trim()),
  enabled: computed(() => tab.value === 'github' && canPick.value),
  staleTime: 30_000,
  retry: false,
})

const picked = ref<GitHubRepo | null>(null)
const pickBranch = ref('')
const pickSubdir = ref('')
const pickAuto = ref(true)
const branches = useQuery({
  queryKey: computed(() => keys.githubBranches(picked.value?.full_name ?? '')),
  queryFn: () => api.githubBranches(picked.value?.full_name ?? ''),
  enabled: computed(() => !!picked.value),
  retry: false,
  staleTime: 60_000,
})
function pick(r: GitHubRepo) {
  picked.value = r
  pickBranch.value = r.default_branch
  pickSubdir.value = ''
  pickAuto.value = true
}
watch(
  () => branches.data.value,
  (b) => {
    if (b && picked.value && !b.branches.includes(pickBranch.value)) pickBranch.value = b.default_branch || b.branches[0] || ''
  },
)

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
  if (!picked.value) return
  link.mutate({
    repo_url: `https://github.com/${picked.value.full_name}`,
    branch: pickBranch.value || undefined,
    subdir: pickSubdir.value.trim() || undefined,
    auto_deploy: pickAuto.value,
  })
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
        <div v-if="github.isPending.value" class="grid gap-2"><Skeleton class="h-8 w-full" /><Skeleton class="h-24 w-full" /></div>
        <div v-else-if="!canPick" class="grid gap-3 rounded-lg border border-dashed p-4 text-sm">
          <p v-if="!configured && !isAdmin" class="text-muted-foreground">GitHub is not set up on this server yet. Ask your admin, or use a repository address.</p>
          <p v-else-if="!configured" class="text-muted-foreground">GitHub is not set up yet. Create the GitHub App on the Admin page first.</p>
          <p v-else class="text-muted-foreground">Connect GitHub to pick one of your repositories and deploy on every push.</p>
          <div v-if="configured || isAdmin">
            <Button as-child variant="outline" size="sm">
              <RouterLink v-if="configured" :to="{ name: 'settings-github' }"><Github /> Connect GitHub</RouterLink>
              <RouterLink v-else :to="{ name: 'admin', query: { tab: 'github' } }">Set up GitHub</RouterLink>
            </Button>
          </div>
        </div>
        <template v-else-if="!picked">
          <div class="relative">
            <Search class="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input v-model="search" class="pl-8" placeholder="Search your repositories" autocomplete="off" spellcheck="false" aria-label="Search your repositories" />
          </div>
          <QueryError v-if="repos.isError.value" :error="repos.error.value" @retry="repos.refetch()" />
          <div v-else-if="repos.isPending.value" class="grid gap-2"><Skeleton v-for="i in 3" :key="i" class="h-10 w-full" /></div>
          <p v-else-if="!repos.data.value?.length" class="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">
            {{ search.trim() ? 'No repository matches that.' : 'Bitploy cannot see any repository yet. Choose repositories on GitHub in Settings > GitHub.' }}
          </p>
          <ul v-else class="max-h-72 divide-y overflow-y-auto rounded-lg border">
            <li v-for="r in repos.data.value" :key="r.full_name">
              <button type="button" class="flex w-full items-center gap-3 px-3 py-2 text-left text-sm hover:bg-muted/60 focus-visible:bg-muted/60 focus-visible:outline-none" @click="pick(r)">
                <span class="min-w-0 flex-1 truncate font-medium">{{ r.full_name }}</span>
                <Badge v-if="r.private" variant="outline"><Lock /> Private</Badge>
                <span v-if="r.pushed_at" class="hidden shrink-0 text-xs text-muted-foreground sm:inline"><RelativeTime :date="r.pushed_at" /></span>
              </button>
            </li>
          </ul>
        </template>
        <div v-else class="grid gap-4">
          <div class="flex items-center gap-3">
            <div class="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-muted/50"><Github class="size-4" /></div>
            <p class="min-w-0 flex-1 truncate text-sm font-medium">{{ picked.full_name }}</p>
            <Button size="sm" variant="ghost" @click="picked = null">Change</Button>
          </div>
          <div class="grid gap-4 sm:grid-cols-2 sm:items-start">
            <div class="grid gap-1.5">
              <Label for="pick-branch">Branch</Label>
              <Select v-if="branches.data.value?.branches.length" v-model="pickBranch">
                <SelectTrigger id="pick-branch" class="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="b in branches.data.value.branches" :key="b" :value="b">{{ b }}</SelectItem>
                </SelectContent>
              </Select>
              <Input v-else id="pick-branch" v-model="pickBranch" autocapitalize="off" autocomplete="off" spellcheck="false" class="font-mono" />
            </div>
            <div class="grid gap-1.5">
              <Label for="pick-subdir">Folder (optional)</Label>
              <Input id="pick-subdir" v-model="pickSubdir" placeholder="Whole repository" autocapitalize="off" autocomplete="off" spellcheck="false" class="font-mono" />
              <p class="text-[0.8rem] text-muted-foreground">Only if the Dockerfile is in a subfolder, like apps/web.</p>
            </div>
          </div>
          <div class="flex items-center justify-between gap-4 text-sm">
            <Label for="pick-auto" class="font-medium">Deploy on every push</Label>
            <Switch id="pick-auto" v-model="pickAuto" />
          </div>
          <div>
            <Button :disabled="link.isPending.value || !pickBranch.trim()" @click="connectPicked">{{ link.isPending.value ? 'Checking...' : 'Connect repository' }}</Button>
          </div>
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
