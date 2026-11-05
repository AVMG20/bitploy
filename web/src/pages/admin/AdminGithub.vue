<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { ExternalLink, Github, TriangleAlert } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyButton from '@/components/CopyButton.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { keys } from '@/composables/queries'
import { api, ApiError } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import type { GitHubManifest } from '@/lib/types'

const route = useRoute()
const router = useRouter()
const qc = useQueryClient()
const info = useQuery({ queryKey: keys.adminGithub, queryFn: api.adminGithub })

const form = useForm({
  validationSchema: toTypedSchema(
    z.object({
      name: z.string().trim().min(1, 'Give the app a name.').max(34, 'GitHub allows at most 34 characters.'),
      organization: z
        .string()
        .trim()
        .max(39, 'That is too long for a GitHub name.')
        .regex(/^[A-Za-z0-9-]*$/, 'Use only letters, numbers and -, like in github.com/<name>.'),
    }),
  ),
  initialValues: { name: '', organization: '' },
})
let named = false
watch(
  () => info.data.value?.default_name,
  (n) => {
    if (n && !named) {
      named = true
      form.setFieldValue('name', n)
    }
  },
  { immediate: true },
)

/** GitHub wants the manifest as a form post, so the browser leaves bitploy for a moment. */
function postToGitHub(m: GitHubManifest) {
  const f = document.createElement('form')
  f.method = 'POST'
  f.action = m.post_url
  f.style.display = 'none'
  const field = document.createElement('input')
  field.type = 'hidden'
  field.name = 'manifest'
  field.value = m.manifest
  f.appendChild(field)
  document.body.appendChild(f)
  f.submit()
}

const start = useMutation({
  mutationFn: (v: { name: string; organization: string }) => api.adminGithubManifest({ name: v.name, organization: v.organization || undefined }),
  onSuccess: postToGitHub,
  onError: (e) => toastError(e),
})
const submit = form.handleSubmit((v) => start.mutate(v))

// GitHub brings the admin back to /admin?code=...&state=...
const finishError = ref<unknown>(null)
const finish = useMutation({
  mutationFn: (q: { code: string; state: string }) => api.adminGithubComplete(q),
  onSuccess: (i) => {
    qc.setQueryData(keys.adminGithub, i)
    void qc.invalidateQueries({ queryKey: keys.github })
    toastOk('GitHub is set up', 'Everyone can connect their GitHub account in Settings > GitHub.')
  },
  onError: (e) => (finishError.value = e),
})
onMounted(async () => {
  const { code, state } = route.query
  if (typeof code === 'string' && code && typeof state === 'string' && state) finish.mutate({ code, state })
  if (code !== undefined || state !== undefined) await router.replace({ name: 'admin', query: { tab: 'github' } })
})

const disconnectOpen = ref(false)
const disconnect = useMutation({
  mutationFn: api.adminGithubDisconnect,
  onSuccess: () => {
    disconnectOpen.value = false
    toastOk('GitHub disconnected', 'Delete the app on GitHub too.')
    named = false
    void qc.invalidateQueries({ queryKey: keys.adminGithub })
    void qc.invalidateQueries({ queryKey: keys.github })
    void qc.invalidateQueries({ queryKey: ['app'] })
  },
  onError: (e) => toastError(e),
})

const finishText = computed(() => (finishError.value instanceof ApiError ? finishError.value.message : 'The GitHub App could not be created.'))
const finishHint = computed(() => (finishError.value instanceof ApiError ? finishError.value.hint : 'Try again.'))
</script>

<template>
  <div class="grid gap-4">
    <QueryError v-if="info.isError.value" :error="info.error.value" @retry="info.refetch()" />
    <Skeleton v-else-if="info.isPending.value" class="h-48 w-full" />
    <template v-else-if="info.data.value">
      <Alert v-if="finish.isPending.value">
        <Github />
        <AlertTitle>Finishing the setup with GitHub...</AlertTitle>
      </Alert>
      <Alert v-else-if="finishError" variant="destructive">
        <TriangleAlert />
        <AlertTitle>{{ finishText }}</AlertTitle>
        <AlertDescription>
          <p>{{ finishHint }}</p>
        </AlertDescription>
      </Alert>
      <Alert v-if="info.data.value.public_url_problem">
        <TriangleAlert />
        <AlertTitle>GitHub cannot reach this server</AlertTitle>
        <AlertDescription>
          <p>
            {{ info.data.value.public_url_problem }}
            <template v-if="info.data.value.configured && !info.data.value.push_events"> Then disconnect GitHub here and create the app again.</template>
          </p>
        </AlertDescription>
      </Alert>

      <Card v-if="!info.data.value.configured">
        <CardHeader>
          <CardTitle>GitHub App</CardTitle>
          <CardDescription>
            Create one GitHub App for this server. Then everyone connects their own GitHub account to it and picks which repositories bitploy may read.
          </CardDescription>
        </CardHeader>
        <form novalidate @submit.prevent="submit">
          <CardContent class="grid gap-4 sm:grid-cols-2 sm:items-start">
            <ol class="grid list-decimal gap-1 pl-5 text-sm text-muted-foreground sm:col-span-2">
              <li>Press Create GitHub App. GitHub opens and shows the app: read-only access to code{{ info.data.value.public_url_problem ? '' : ', and push events' }}.</li>
              <li>Confirm on GitHub. You come back here, and bitploy stores the app's keys encrypted.</li>
              <li>Everyone can then connect their GitHub account in Settings > GitHub.</li>
            </ol>
            <FormField v-slot="{ componentField }" name="name">
              <FormItem>
                <FormLabel>App name on GitHub</FormLabel>
                <FormControl><Input autocomplete="off" v-bind="componentField" /></FormControl>
                <FormDescription>It has to be unique on GitHub.</FormDescription>
                <FormMessage />
              </FormItem>
            </FormField>
            <FormField v-slot="{ componentField }" name="organization">
              <FormItem>
                <FormLabel>GitHub organization (optional)</FormLabel>
                <FormControl><Input placeholder="my-company" autocapitalize="off" autocomplete="off" spellcheck="false" v-bind="componentField" /></FormControl>
                <FormDescription>Leave empty to create the app under your own account.</FormDescription>
                <FormMessage />
              </FormItem>
            </FormField>
          </CardContent>
          <CardFooter class="flex-wrap justify-between gap-3">
            <p class="text-xs text-muted-foreground">Sign in to GitHub first with the account that should own the app.</p>
            <Button type="submit" :disabled="start.isPending.value || finish.isPending.value">
              <Github /> {{ start.isPending.value ? 'Opening GitHub...' : 'Create GitHub App' }}
            </Button>
          </CardFooter>
        </form>
      </Card>

      <Card v-else>
        <CardHeader>
          <CardTitle>{{ info.data.value.name }}</CardTitle>
          <CardDescription>
            {{
              info.data.value.push_events
                ? 'GitHub is set up. Everyone can connect their account in Settings > GitHub, and pushes deploy their apps.'
                : 'GitHub is set up. Everyone can connect their account in Settings > GitHub to deploy private repositories.'
            }}
          </CardDescription>
        </CardHeader>
        <CardContent class="grid gap-4">
          <dl class="grid grid-cols-[9rem_minmax(0,1fr)] gap-x-4 gap-y-2.5 text-sm">
            <dt class="text-muted-foreground">App on GitHub</dt>
            <dd class="min-w-0">
              <a :href="info.data.value.html_url" target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-1 hover:underline">
                {{ info.data.value.slug }} <ExternalLink class="size-3.5" />
              </a>
            </dd>
            <template v-if="info.data.value.owner">
              <dt class="text-muted-foreground">Owned by</dt>
              <dd>@{{ info.data.value.owner }}</dd>
            </template>
            <dt class="text-muted-foreground">Push events</dt>
            <dd v-if="info.data.value.push_events" class="flex min-w-0 items-center gap-1">
              <code class="min-w-0 truncate font-mono text-xs">{{ info.data.value.webhook_url }}</code>
              <CopyButton v-if="info.data.value.webhook_url" :value="info.data.value.webhook_url" what="Address copied" label="Copy address" icon />
            </dd>
            <dd v-else class="text-muted-foreground">Off. GitHub could not reach this server when the app was created.</dd>
            <template v-if="info.data.value.created_at">
              <dt class="text-muted-foreground">Created</dt>
              <dd><RelativeTime :date="info.data.value.created_at" /></dd>
            </template>
          </dl>
        </CardContent>
        <CardFooter class="flex-wrap justify-between gap-3">
          <Button as-child variant="outline">
            <a :href="info.data.value.manage_url" target="_blank" rel="noopener noreferrer">Manage on GitHub <ExternalLink /></a>
          </Button>
          <Button variant="outline" class="text-destructive" @click="disconnectOpen = true">Disconnect</Button>
        </CardFooter>
      </Card>
    </template>

    <ConfirmDialog
      v-model:open="disconnectOpen"
      title="Disconnect GitHub?"
      description="bitploy forgets the app's keys and every connected GitHub account. Private repositories cannot be built and pushes stop deploying until you create the app again. This does not delete the app on GitHub: do that yourself under Manage on GitHub, Advanced, Delete GitHub App."
      confirm-label="Disconnect"
      :loading="disconnect.isPending.value"
      @confirm="disconnect.mutate()"
    />
  </div>
</template>
