<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { refDebounced } from '@vueuse/core'
import { Github, Lock, Search } from 'lucide-vue-next'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { keys } from '@/composables/queries'
import { useSession } from '@/composables/useSession'
import { api } from '@/lib/api'
import type { GitHubRepo, GitSourceInput } from '@/lib/types'

// Pick one of the user's GitHub repositories, its branch and whether pushes
// deploy. v-model is the choice as the git-source call wants it, or null while
// nothing is picked. Used by "Connect a repository" in the app settings and by
// the New app form.
// `start` opens it with that repository picked (the New project page picks on its first step).
const props = withDefaults(defineProps<{ compact?: boolean; showFolder?: boolean; idPrefix?: string; start?: GitHubRepo | null }>(), { showFolder: true, idPrefix: 'pick', start: null })
const emit = defineEmits<{ picked: [repo: GitHubRepo] }>()
const model = defineModel<GitSourceInput | null>({ default: null })
const { isAdmin } = useSession()

const github = useQuery({ queryKey: keys.github, queryFn: api.github })
const configured = computed(() => github.data.value?.app.configured ?? false)
const canPick = computed(() => (github.data.value?.installations ?? []).some((i) => i.status === 'active'))

const search = ref('')
const debounced = refDebounced(search, 250)
const repos = useQuery({
  queryKey: computed(() => keys.githubRepos(debounced.value.trim())),
  queryFn: () => api.githubRepos(debounced.value.trim()),
  enabled: canPick,
  staleTime: 30_000,
  retry: false,
})

const picked = ref<GitHubRepo | null>(null)
const branch = ref('')
const subdir = ref('')
const auto = ref(true)
const branches = useQuery({
  queryKey: computed(() => keys.githubBranches(picked.value?.full_name ?? '')),
  queryFn: () => api.githubBranches(picked.value?.full_name ?? ''),
  enabled: computed(() => !!picked.value),
  retry: false,
  staleTime: 60_000,
})

function pick(r: GitHubRepo) {
  picked.value = r
  branch.value = r.default_branch
  subdir.value = ''
  auto.value = true
  emit('picked', r)
}
// Main or master first, when the repository's own default is not known to exist.
watch(
  () => branches.data.value,
  (b) => {
    if (b && picked.value && !b.branches.includes(branch.value)) {
      branch.value = b.default_branch || b.branches.find((x) => x === 'main' || x === 'master') || b.branches[0] || ''
    }
  },
)
watch(
  [picked, branch, subdir, auto],
  () => {
    model.value = picked.value && branch.value.trim()
      ? {
          repo_url: `https://github.com/${picked.value.full_name}`,
          branch: branch.value.trim(),
          subdir: props.showFolder ? subdir.value.trim() || undefined : undefined,
          auto_deploy: auto.value,
        }
      : null
  },
  { flush: 'sync' },
)
if (props.start) pick(props.start)
defineExpose({ canPick, configured, loading: computed(() => github.isPending.value) })
</script>

<template>
  <div class="grid gap-4">
    <QueryError v-if="github.isError.value" :error="github.error.value" @retry="github.refetch()" />
    <div v-else-if="github.isPending.value" class="grid gap-2"><Skeleton class="h-8 w-full" /><Skeleton class="h-24 w-full" /></div>

    <template v-else-if="!canPick">
      <p v-if="compact" class="text-sm text-muted-foreground">
        <template v-if="configured">
          <RouterLink :to="{ name: 'settings-github' }" class="font-medium text-foreground underline underline-offset-4">Connect GitHub</RouterLink> in Settings to deploy a repository.
        </template>
        <template v-else-if="isAdmin">
          GitHub is not set up on this server.
          <RouterLink :to="{ name: 'admin', query: { tab: 'github' } }" class="font-medium text-foreground underline underline-offset-4">Set it up</RouterLink> on the Admin page first, then connect it in Settings > GitHub.
        </template>
        <template v-else>GitHub is not set up on this server. Ask your admin, or upload your files.</template>
      </p>
      <div v-else class="grid gap-3 rounded-lg border border-dashed p-4 text-sm">
        <p v-if="!configured && !isAdmin" class="text-muted-foreground">GitHub is not set up on this server. Ask your admin, or use a repository address.</p>
        <p v-else-if="!configured" class="text-muted-foreground">GitHub is not set up on this server. Create the GitHub App on the Admin page first.</p>
        <p v-else class="text-muted-foreground">Connect GitHub to pick one of your repositories and deploy on every push.</p>
        <div v-if="configured || isAdmin">
          <Button as-child variant="outline" size="sm">
            <RouterLink v-if="configured" :to="{ name: 'settings-github' }"><Github /> Connect GitHub</RouterLink>
            <RouterLink v-else :to="{ name: 'admin', query: { tab: 'github' } }">Set up GitHub</RouterLink>
          </Button>
        </div>
      </div>
    </template>

    <template v-else-if="!picked">
      <div class="relative">
        <Search class="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input v-model="search" class="pl-8" placeholder="Search your repositories" autocomplete="off" spellcheck="false" aria-label="Search your repositories" />
      </div>
      <QueryError v-if="repos.isError.value" :error="repos.error.value" @retry="repos.refetch()" />
      <div v-else-if="repos.isPending.value" class="grid gap-2"><Skeleton v-for="i in 3" :key="i" class="h-10 w-full" /></div>
      <p v-else-if="!repos.data.value?.length" class="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">
        {{ search.trim() ? 'No repository matches that.' : 'bitploy cannot read any of your repositories. Choose which ones it may read in Settings > GitHub.' }}
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
        <Button type="button" size="sm" variant="ghost" @click="picked = null">Change</Button>
      </div>
      <div class="grid gap-4 sm:items-start" :class="showFolder && 'sm:grid-cols-2'">
        <div class="grid gap-1.5">
          <Label :for="`${idPrefix}-branch`">Branch</Label>
          <Select v-if="branches.data.value?.branches.length" v-model="branch">
            <SelectTrigger :id="`${idPrefix}-branch`" class="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem v-for="b in branches.data.value.branches" :key="b" :value="b">{{ b }}</SelectItem>
            </SelectContent>
          </Select>
          <Input v-else :id="`${idPrefix}-branch`" v-model="branch" autocapitalize="off" autocomplete="off" spellcheck="false" class="font-mono" />
        </div>
        <div v-if="showFolder" class="grid gap-1.5">
          <Label :for="`${idPrefix}-subdir`">Folder (optional)</Label>
          <Input :id="`${idPrefix}-subdir`" v-model="subdir" placeholder="Whole repository" autocapitalize="off" autocomplete="off" spellcheck="false" class="font-mono" />
          <p class="text-[0.8rem] text-muted-foreground">Only if the Dockerfile is in a subfolder, like apps/web.</p>
        </div>
      </div>
      <div class="flex items-center justify-between gap-4 text-sm">
        <Label :for="`${idPrefix}-auto`" class="font-medium">Deploy on every push</Label>
        <Switch :id="`${idPrefix}-auto`" v-model="auto" />
      </div>
    </div>
  </div>
</template>
