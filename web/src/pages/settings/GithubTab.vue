<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { Building2, ExternalLink, Github, TriangleAlert } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import QueryError from '@/components/QueryError.vue'
import SettingsSection from '@/components/SettingsSection.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { keys } from '@/composables/queries'
import { useSession } from '@/composables/useSession'
import { api, ApiError } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import type { GitHubInstallation } from '@/lib/types'

const route = useRoute()
const router = useRouter()
const qc = useQueryClient()
const { isAdmin } = useSession()
const github = useQuery({ queryKey: keys.github, queryFn: api.github })
const installations = computed(() => github.data.value?.installations ?? [])
const configured = computed(() => github.data.value?.app.configured ?? false)

// GitHub sends the browser back here with ?code=...&state=... (and installation_id).
// The server checks them with GitHub; nothing in the address is trusted by itself.
const linkError = ref<unknown>(null)
const waitingForOwner = ref(false)
const link = useMutation({
  mutationFn: (q: { installation_id?: number; code: string; state: string }) => api.githubLink(q),
  onSuccess: (ov) => {
    qc.setQueryData(keys.github, ov)
    toastOk(ov.installations.length === 1 ? `Connected @${ov.installations[0]!.account_login}` : 'GitHub connected')
    void qc.invalidateQueries({ queryKey: ['app'] })
    void qc.invalidateQueries({ queryKey: ['github', 'repos'] })
  },
  onError: (e) => (linkError.value = e),
})

function one(v: unknown): string {
  return typeof v === 'string' ? v : ''
}
onMounted(async () => {
  const { code, state, installation_id: inst, setup_action: action } = route.query
  const hadParams = code !== undefined || state !== undefined || inst !== undefined || action !== undefined
  if (one(code) && one(state)) {
    const id = Number(one(inst))
    link.mutate({ installation_id: Number.isSafeInteger(id) && id > 0 ? id : undefined, code: one(code), state: one(state) })
  } else if (one(action) === 'request') {
    waitingForOwner.value = true
  }
  if (hadParams) await router.replace({ name: 'settings-github' })
})

const connect = useMutation({
  mutationFn: (mode: 'install' | 'authorize') => api.githubConnect(mode),
  onSuccess: ({ url }) => window.location.assign(url),
  onError: (e) => toastError(e),
})

const toRemove = ref<GitHubInstallation | null>(null)
const removeOpen = computed({
  get: () => toRemove.value !== null,
  set: (v) => {
    if (!v) toRemove.value = null
  },
})
const remove = useMutation({
  mutationFn: (id: number) => api.githubUnlink(id),
  onSuccess: () => {
    toastOk('Account disconnected')
    toRemove.value = null
    void qc.invalidateQueries({ queryKey: keys.github })
    void qc.invalidateQueries({ queryKey: ['app'] })
  },
  onError: (e) => toastError(e),
})

function describe(i: GitHubInstallation): string {
  if (i.status === 'deleted') return 'Bitploy was removed on GitHub'
  if (i.status === 'suspended') return 'Paused on GitHub'
  if (i.repository_selection === 'all') return 'All repositories'
  if (i.repo_count === undefined) return 'Selected repositories'
  return i.repo_count === 1 ? '1 repository' : `${i.repo_count} repositories`
}

const linkErrorText = computed(() => (linkError.value instanceof ApiError ? linkError.value.message : 'GitHub could not be connected.'))
const linkErrorHint = computed(() => (linkError.value instanceof ApiError ? linkError.value.hint : 'Try again.'))
</script>

<template>
  <SettingsSection title="GitHub" description="Connect GitHub to deploy private repositories, and to deploy again on every push.">
    <div class="grid gap-4">
      <QueryError v-if="github.isError.value" :error="github.error.value" @retry="github.refetch()" />
      <Skeleton v-else-if="github.isPending.value" class="h-40 w-full" />
      <template v-else>
        <Alert v-if="link.isPending.value">
          <Github />
          <AlertTitle>Connecting your GitHub account...</AlertTitle>
        </Alert>
        <Alert v-else-if="linkError" variant="destructive">
          <TriangleAlert />
          <AlertTitle>{{ linkErrorText }}</AlertTitle>
          <AlertDescription>
            <p>{{ linkErrorHint }}</p>
          </AlertDescription>
        </Alert>
        <Alert v-else-if="waitingForOwner">
          <Github />
          <AlertTitle>Waiting for an owner of the organization</AlertTitle>
          <AlertDescription>
            <p>GitHub sent them your request. When they approve it, press Connect GitHub again.</p>
          </AlertDescription>
        </Alert>

        <Card v-if="!configured">
          <CardHeader>
            <CardTitle>GitHub is not set up yet</CardTitle>
            <CardDescription v-if="isAdmin">Create the GitHub App once on the Admin page. After that everyone can connect their own account.</CardDescription>
            <CardDescription v-else>Ask your bitploy admin to set it up. Until then private repositories cannot be deployed. Public ones work: use their address in the app's Settings and deploy by hand.</CardDescription>
          </CardHeader>
          <CardContent v-if="isAdmin">
            <Button as-child><RouterLink :to="{ name: 'admin', query: { tab: 'github' } }">Set up GitHub</RouterLink></Button>
          </CardContent>
        </Card>

        <Card v-else>
          <CardHeader>
            <CardTitle>Connected accounts</CardTitle>
            <CardDescription>Bitploy can only read the repositories you choose on GitHub.</CardDescription>
          </CardHeader>
          <CardContent class="grid gap-3">
            <ul v-if="installations.length" class="divide-y rounded-lg border">
              <li v-for="i in installations" :key="i.id" class="flex flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2.5">
                <div class="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-muted/50">
                  <component :is="i.account_type === 'Organization' ? Building2 : Github" class="size-4" />
                </div>
                <div class="min-w-0 text-sm">
                  <p class="truncate font-medium">@{{ i.account_login }}</p>
                  <p class="text-xs text-muted-foreground">{{ describe(i) }}</p>
                </div>
                <StatusBadge v-if="i.status === 'suspended'" label="Paused" tone="warning" />
                <StatusBadge v-else-if="i.status === 'deleted'" label="Removed" tone="danger" />
                <div class="ml-auto flex flex-wrap gap-1">
                  <Button v-if="i.status !== 'deleted'" as-child size="sm" variant="outline">
                    <a :href="i.manage_url" target="_blank" rel="noopener noreferrer">Choose repos on GitHub <ExternalLink /></a>
                  </Button>
                  <Button size="sm" variant="ghost" @click="toRemove = i">Disconnect</Button>
                </div>
              </li>
            </ul>
            <p v-else class="text-sm text-muted-foreground">No account connected yet. Connect one and pick the repositories bitploy may read.</p>
            <p v-if="installations.some((i) => i.status === 'deleted')" class="text-xs text-muted-foreground">
              Bitploy was removed on GitHub for some accounts. Connect them again, or disconnect them here.
            </p>
          </CardContent>
          <CardFooter class="flex-wrap justify-between gap-x-4 gap-y-2">
            <p class="text-xs text-muted-foreground">
              Already installed on GitHub?
              <button type="button" class="font-medium text-foreground underline-offset-2 hover:underline" :disabled="connect.isPending.value" @click="connect.mutate('authorize')">
                Link existing accounts
              </button>
            </p>
            <Button :variant="installations.length ? 'outline' : 'default'" :disabled="connect.isPending.value" @click="connect.mutate('install')">
              <Github /> {{ installations.length ? 'Connect another account' : 'Connect GitHub' }}
            </Button>
          </CardFooter>
        </Card>

        <p class="text-xs text-muted-foreground">
          Public repositories need no connection. Pushes to a connected repository deploy the app by themselves; turn that off per app in its Settings.
        </p>
      </template>
    </div>

    <ConfirmDialog
      v-model:open="removeOpen"
      title="Disconnect this account?"
      :description="`Bitploy forgets @${toRemove?.account_login ?? ''}. Apps from its private repositories can no longer be built, and pushes stop deploying. To remove the access on GitHub too, uninstall bitploy there.`"
      confirm-label="Disconnect"
      :loading="remove.isPending.value"
      @confirm="toRemove && remove.mutate(toRemove.id)"
    />
  </SettingsSection>
</template>
