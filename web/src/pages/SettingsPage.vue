<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Github, LogOut, Monitor } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import PageHeader from '@/components/PageHeader.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { keys } from '@/composables/queries'
import { SESSION_KEY, useSession } from '@/composables/useSession'
import { api } from '@/lib/api'
import { exactTime } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import { queryClient } from '@/lib/queryClient'
import type { SessionInfo } from '@/lib/types'

const router = useRouter()
const qc = useQueryClient()
const { user } = useSession()

// ---------------------------------------------------------------- profile
function emailChanged(email: string | undefined): boolean {
  const current = user.value?.email.toLowerCase() ?? ''
  return !!email && email.trim().toLowerCase() !== current
}
const profile = useForm({
  validationSchema: toTypedSchema(
    z
      .object({
        name: z.string().trim().min(1, 'Tell us your name.'),
        email: z.string().min(1, 'Enter your email address.').email('That does not look like an email address.'),
        password: z.string().optional(),
      })
      .refine((v) => !emailChanged(v.email) || !!v.password, {
        path: ['password'],
        message: 'Enter your current password to change your email address.',
      }),
  ),
  initialValues: { name: '', email: '', password: '' },
})
const needsPassword = computed(() => emailChanged(profile.values.email))
let profileLoaded = false
watch(
  user,
  (u) => {
    if (u && !profileLoaded) {
      profileLoaded = true
      profile.resetForm({ values: { name: u.name, email: u.email, password: '' } })
    }
  },
  { immediate: true },
)
const saveProfile = useMutation({
  mutationFn: (v: { name: string; email: string; password?: string }) =>
    api.updateProfile(
      emailChanged(v.email)
        ? { name: v.name.trim(), email: v.email.trim(), current_password: v.password }
        : { name: v.name.trim() },
    ),
  onSuccess: (u) => {
    toastOk('Profile saved')
    profile.resetForm({ values: { name: u.name, email: u.email, password: '' } })
    // Update the cached user at once, or the form would see the old email until the refetch lands.
    qc.setQueryData<SessionInfo>(SESSION_KEY, (old) => (old ? { ...old, user: u } : old))
    void qc.invalidateQueries({ queryKey: SESSION_KEY })
    void qc.invalidateQueries({ queryKey: keys.me })
  },
  onError: (e) => toastError(e),
})
const submitProfile = profile.handleSubmit((v) => saveProfile.mutate(v))

// ---------------------------------------------------------------- password
const pw = useForm({
  validationSchema: toTypedSchema(
    z
      .object({
        current: z.string().min(1, 'Enter your current password.'),
        next: z.string().min(10, 'Use at least 10 characters.'),
        confirm: z.string(),
      })
      .refine((v) => v.next === v.confirm, { path: ['confirm'], message: 'The two passwords are not the same.' }),
  ),
  initialValues: { current: '', next: '', confirm: '' },
})
const savePw = useMutation({
  mutationFn: (v: { current: string; next: string }) => api.changePassword({ current_password: v.current, new_password: v.next }),
  onSuccess: () => {
    toastOk('Password changed', 'You were signed out on your other devices.')
    pw.resetForm()
    void qc.invalidateQueries({ queryKey: keys.sessions })
  },
  onError: (e) => toastError(e),
})
const submitPw = pw.handleSubmit((v) => savePw.mutate(v))

// ---------------------------------------------------------------- github
const github = useQuery({ queryKey: keys.github, queryFn: api.githubStatus })
const gh = useForm({
  validationSchema: toTypedSchema(z.object({ token: z.string().trim().min(10, 'Paste the full token from GitHub.') })),
  initialValues: { token: '' },
})
const saveGh = useMutation({
  mutationFn: (token: string) => api.saveGithubToken(token),
  onSuccess: (s) => {
    toastOk(s?.login ? `Connected as @${s.login}` : 'GitHub token saved')
    gh.resetForm()
    void qc.invalidateQueries({ queryKey: keys.github })
  },
  onError: (e) => toastError(e),
})
const submitGh = gh.handleSubmit((v) => saveGh.mutate(v.token))
const ghRemoveOpen = ref(false)
const removeGh = useMutation({
  mutationFn: api.removeGithubToken,
  onSuccess: () => {
    toastOk('GitHub token removed')
    ghRemoveOpen.value = false
    void qc.invalidateQueries({ queryKey: keys.github })
  },
  onError: (e) => toastError(e),
})

// ---------------------------------------------------------------- sessions
const sessions = useQuery({ queryKey: keys.sessions, queryFn: api.sessions })
const revokeOne = useMutation({
  mutationFn: (id: string) => api.revokeSession(id),
  onSuccess: () => {
    toastOk('Device signed out')
    void qc.invalidateQueries({ queryKey: keys.sessions })
  },
  onError: (e) => toastError(e),
})
const everywhereOpen = ref(false)
const everywhere = useMutation({
  mutationFn: async () => {
    await api.revokeOtherSessions()
    await api.logout()
  },
  onSuccess: async () => {
    queryClient.clear()
    await router.replace({ name: 'login' })
  },
  onError: (e) => toastError(e),
})

function deviceName(ua: string): string {
  if (!ua) return 'Unknown device'
  const browser = /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Firefox\//.test(ua) ? 'Firefox' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
  const os = /Windows/.test(ua) ? 'Windows' : /Android/.test(ua) ? 'Android' : /iPhone|iPad/.test(ua) ? 'iOS' : /Mac OS X/.test(ua) ? 'macOS' : /Linux/.test(ua) ? 'Linux' : ''
  return os ? `${browser} on ${os}` : browser
}
const otherCount = computed(() => (sessions.data.value ?? []).filter((s) => !s.current).length)
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Settings" description="Your account, password and connections." />

    <Card>
      <CardHeader><CardTitle>Profile</CardTitle></CardHeader>
      <CardContent>
        <form class="grid max-w-md gap-4" novalidate @submit.prevent="submitProfile">
          <FormField v-slot="{ componentField }" name="name">
            <FormItem><FormLabel>Name</FormLabel><FormControl><Input autocomplete="name" v-bind="componentField" /></FormControl><FormMessage /></FormItem>
          </FormField>
          <FormField v-slot="{ componentField }" name="email">
            <FormItem><FormLabel>Email</FormLabel><FormControl><Input type="email" autocomplete="email" v-bind="componentField" /></FormControl><FormMessage /></FormItem>
          </FormField>
          <FormField v-if="needsPassword" v-slot="{ componentField }" name="password">
            <FormItem>
              <FormLabel>Current password</FormLabel>
              <FormControl><Input type="password" autocomplete="current-password" v-bind="componentField" /></FormControl>
              <FormDescription>Needed to change your email address.</FormDescription>
              <FormMessage />
            </FormItem>
          </FormField>
          <div><Button type="submit" :disabled="saveProfile.isPending.value">{{ saveProfile.isPending.value ? 'Saving...' : 'Save profile' }}</Button></div>
        </form>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle>Password</CardTitle>
        <CardDescription>Changing it signs you out on all your other devices.</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="grid max-w-md gap-4" novalidate @submit.prevent="submitPw">
          <FormField v-slot="{ componentField }" name="current">
            <FormItem><FormLabel>Current password</FormLabel><FormControl><Input type="password" autocomplete="current-password" v-bind="componentField" /></FormControl><FormMessage /></FormItem>
          </FormField>
          <FormField v-slot="{ componentField }" name="next">
            <FormItem>
              <FormLabel>New password</FormLabel>
              <FormControl><Input type="password" autocomplete="new-password" v-bind="componentField" /></FormControl>
              <FormDescription>At least 10 characters.</FormDescription>
              <FormMessage />
            </FormItem>
          </FormField>
          <FormField v-slot="{ componentField }" name="confirm">
            <FormItem><FormLabel>New password again</FormLabel><FormControl><Input type="password" autocomplete="new-password" v-bind="componentField" /></FormControl><FormMessage /></FormItem>
          </FormField>
          <div><Button type="submit" :disabled="savePw.isPending.value">{{ savePw.isPending.value ? 'Changing...' : 'Change password' }}</Button></div>
        </form>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle>GitHub</CardTitle>
        <CardDescription>
          Needed only to deploy private repositories. Create a fine-grained personal access token on GitHub with read-only access to "Contents" and "Metadata" for the repositories you choose.
        </CardDescription>
      </CardHeader>
      <CardContent class="grid max-w-xl gap-4">
        <QueryError v-if="github.isError.value" :error="github.error.value" @retry="github.refetch()" />
        <Skeleton v-else-if="github.isPending.value" class="h-10 w-full" />
        <div v-else-if="github.data.value?.connected" class="flex flex-wrap items-center gap-2 rounded-lg border p-3">
          <Github class="size-4" />
          <span class="text-sm">Connected as <strong>@{{ github.data.value.login }}</strong></span>
          <span v-if="github.data.value.expires_at" class="text-xs text-muted-foreground" :title="exactTime(github.data.value.expires_at)">
            expires <RelativeTime :date="github.data.value.expires_at" />
          </span>
          <Button class="ml-auto" size="sm" variant="outline" @click="ghRemoveOpen = true">Disconnect</Button>
        </div>
        <p v-else class="text-sm text-muted-foreground">Not connected.</p>

        <form class="grid gap-3" novalidate @submit.prevent="submitGh">
          <FormField v-slot="{ componentField }" name="token">
            <FormItem>
              <FormLabel>{{ github.data.value?.connected ? 'Replace the token' : 'Personal access token' }}</FormLabel>
              <FormControl><Input type="password" placeholder="github_pat_..." autocomplete="off" spellcheck="false" v-bind="componentField" /></FormControl>
              <FormDescription>We check it with GitHub when you save, and store it encrypted. It is never shown again.</FormDescription>
              <FormMessage />
            </FormItem>
          </FormField>
          <div><Button type="submit" :disabled="saveGh.isPending.value">{{ saveGh.isPending.value ? 'Checking...' : 'Save token' }}</Button></div>
        </form>
      </CardContent>
    </Card>

    <Card>
      <CardHeader class="flex flex-row flex-wrap items-start justify-between gap-2">
        <div>
          <CardTitle>Where you are logged in</CardTitle>
          <CardDescription>If you see a device you do not know, sign it out and change your password.</CardDescription>
        </div>
        <Button variant="outline" size="sm" @click="everywhereOpen = true"><LogOut /> Sign out everywhere</Button>
      </CardHeader>
      <CardContent>
        <QueryError v-if="sessions.isError.value" :error="sessions.error.value" @retry="sessions.refetch()" />
        <Skeleton v-else-if="sessions.isPending.value" class="h-16 w-full" />
        <ul v-else class="divide-y rounded-lg border">
          <li v-for="s in sessions.data.value" :key="s.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2.5">
            <Monitor class="size-4 text-muted-foreground" />
            <div class="min-w-0">
              <p class="text-sm font-medium">
                {{ deviceName(s.user_agent) }}
                <Badge v-if="s.current" variant="secondary" class="ml-1">This device</Badge>
              </p>
              <p class="text-xs text-muted-foreground">{{ s.ip }} &middot; last active <RelativeTime :date="s.last_seen_at" /> &middot; signed in <RelativeTime :date="s.created_at" /></p>
            </div>
            <Button v-if="!s.current" class="ml-auto" size="sm" variant="ghost" :disabled="revokeOne.isPending.value" :aria-label="`Sign out ${deviceName(s.user_agent)}`" @click="revokeOne.mutate(s.id)">Sign out</Button>
          </li>
        </ul>
      </CardContent>
    </Card>

    <ConfirmDialog
      v-model:open="ghRemoveOpen"
      title="Disconnect GitHub?"
      description="Bitploy forgets the token. Deploys from private repositories stop working until you add a token again."
      confirm-label="Disconnect"
      :loading="removeGh.isPending.value"
      @confirm="removeGh.mutate()"
    />
    <ConfirmDialog
      v-model:open="everywhereOpen"
      title="Sign out everywhere?"
      :description="`You will be logged out on this device${otherCount ? ` and ${otherCount} other${otherCount === 1 ? '' : 's'}` : ''}. API tokens keep working.`"
      confirm-label="Sign out everywhere"
      :loading="everywhere.isPending.value"
      @confirm="everywhere.mutate()"
    />
  </div>
</template>
