<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import AuthShell from '@/components/AuthShell.vue'
import QueryError from '@/components/QueryError.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { SESSION_KEY } from '@/composables/useSession'
import { api, ApiError } from '@/lib/api'
import { forgetThisBrowser } from '@/lib/push'
import { keys } from '@/lib/live'
import { projectRoute } from '@/lib/appLinks'
import { queryClient } from '@/lib/queryClient'
import { toastOk } from '@/lib/notify'
import type { SessionInfo } from '@/lib/types'

// An invite to someone's project (/join/<code>). Logged in: one click joins
// it. Not logged in: log in first (and come back here), or make an account
// with the link, which joins the project at the same time. That last one only
// while the server lets project invites create accounts (Admin > Signups).
const route = useRoute()
const router = useRouter()
const code = computed(() => String(route.params.code))

const info = useQuery({
  queryKey: computed(() => ['project-invite', code.value]),
  queryFn: () => api.projectInviteInfo(code.value),
  retry: false,
})
// The router already asked who is logged in; null when nobody.
const session = useQuery({
  queryKey: SESSION_KEY,
  queryFn: async (): Promise<SessionInfo | null> => {
    try {
      return await api.session()
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return null
      throw e
    }
  },
  staleTime: 5 * 60_000,
  retry: false,
})
const user = computed(() => session.data.value?.user ?? null)
/** A link that is wrong, used or expired (not a server or network hiccup, which can be retried). */
const badLink = computed(() => {
  const e = info.error.value
  return e instanceof ApiError && e.status >= 400 && e.status < 500 ? e : null
})
const lockedElsewhere = computed(() => {
  const e = info.data.value?.email
  return !!e && !!user.value && e.toLowerCase() !== user.value.email.toLowerCase()
})
const loginTo = computed(() => ({ name: 'login', query: { next: route.fullPath } }))
const canSignUp = computed(() => info.data.value?.signup === true)

const title = computed(() => (info.data.value ? `Join ${info.data.value.project_name}` : 'Join a project'))
const description = computed(() => {
  const i = info.data.value
  if (!i) return undefined
  const who = i.invited_by || 'Someone'
  return `${who} invited you to the project ${i.project_name} on bitploy. You can deploy and change its apps once you join.`
})

const failure = ref<ApiError | null>(null)
const busy = ref(false)
function fail(e: unknown, fallback: string) {
  failure.value = e instanceof ApiError ? e : new ApiError(0, { message: fallback })
}
async function arrive(projectId: string) {
  await queryClient.invalidateQueries({ queryKey: keys.apps })
  await router.replace(projectRoute(projectId))
}

async function join() {
  if (busy.value) return
  busy.value = true
  failure.value = null
  try {
    const p = await api.acceptProjectInvite(code.value)
    toastOk(`You joined ${p.name}`)
    await arrive(p.id)
  } catch (e) {
    fail(e, 'Could not join the project.')
  } finally {
    busy.value = false
  }
}

async function useOtherAccount() {
  await forgetThisBrowser()
  await api.logout().catch(() => undefined)
  queryClient.setQueryData(SESSION_KEY, null)
  await router.push(loginTo.value)
}

const { handleSubmit, setFieldValue } = useForm({
  validationSchema: toTypedSchema(
    z
      .object({
        name: z.string().trim().min(1, 'Enter your name.'),
        email: z.string().trim().min(1, 'Enter your email address.').email('That does not look like an email address.'),
        password: z.string().min(10, 'Use at least 10 characters.'),
        confirm: z.string(),
      })
      .refine((v) => v.password === v.confirm, { path: ['confirm'], message: 'The two passwords are not the same.' }),
  ),
})
// An invite for one address: that is the address of the new account.
const lockedEmail = computed(() => info.data.value?.email ?? '')
watch(
  lockedEmail,
  (e) => {
    if (e) setFieldValue('email', e)
  },
  { immediate: true },
)

const signup = handleSubmit(async (values) => {
  if (busy.value) return
  busy.value = true
  failure.value = null
  try {
    const out = await api.signupProjectInvite(code.value, { name: values.name.trim(), email: values.email.trim(), password: values.password })
    toastOk('Welcome to bitploy', `You joined ${out.project.name}.`)
    await queryClient.fetchQuery({ queryKey: SESSION_KEY, queryFn: api.session, staleTime: 0 })
    await arrive(out.project.id)
  } catch (e) {
    fail(e, 'Could not create your account.')
    // The admin stopped project invites from making accounts after the page loaded.
    if (e instanceof ApiError && e.code === 'project_invite_signup_off') void info.refetch()
  } finally {
    busy.value = false
  }
})
</script>

<template>
  <AuthShell :title="title" :description="description">
    <div class="grid gap-4">
      <div v-if="info.isPending.value || session.isPending.value" class="grid gap-3">
        <Skeleton class="h-10 w-full" />
        <Skeleton class="h-10 w-full" />
      </div>

      <template v-else-if="info.isError.value">
        <Alert v-if="badLink" variant="destructive">
          <AlertTitle>{{ badLink.message }}</AlertTitle>
          <AlertDescription>{{ badLink.hint || 'Ask the person who invited you for a new link.' }}</AlertDescription>
        </Alert>
        <QueryError v-else :error="info.error.value" @retry="info.refetch()" />
        <Button variant="outline" as-child class="w-full"><RouterLink :to="user ? '/' : '/login'">{{ user ? 'Go to your projects' : 'Log in' }}</RouterLink></Button>
      </template>

      <template v-else-if="user">
        <Alert v-if="failure" variant="destructive">
          <AlertTitle>{{ failure.message }}</AlertTitle>
          <AlertDescription v-if="failure.hint">{{ failure.hint }}</AlertDescription>
        </Alert>
        <Alert v-if="lockedElsewhere">
          <AlertTitle>This invite is for {{ info.data.value?.email }}</AlertTitle>
          <AlertDescription>You are logged in as {{ user.email }}. Log in with the other account to join.</AlertDescription>
        </Alert>
        <p v-else class="text-sm text-muted-foreground">You are logged in as <span class="font-medium text-foreground">{{ user.email }}</span>.</p>
        <Button v-if="!lockedElsewhere" class="w-full" :disabled="busy" @click="join">{{ busy ? 'Joining...' : 'Join project' }}</Button>
        <Button variant="outline" class="w-full" @click="useOtherAccount">Use another account</Button>
      </template>

      <template v-else>
        <Alert v-if="!canSignUp">
          <AlertTitle>You need an account on this server first</AlertTitle>
          <AlertDescription>This invite only works for people who already have an account here. To make one, ask an admin of this server for an invite link, then open this link again.</AlertDescription>
        </Alert>
        <Button as-child class="w-full"><RouterLink :to="loginTo">I have an account: log in</RouterLink></Button>
        <div v-if="canSignUp" class="flex items-center gap-3 text-xs text-muted-foreground">
          <span class="h-px flex-1 bg-border" />
          or make an account
          <span class="h-px flex-1 bg-border" />
        </div>
        <form v-if="canSignUp" class="grid gap-4" novalidate @submit.prevent="signup">
          <Alert v-if="failure" variant="destructive">
            <AlertTitle>{{ failure.message }}</AlertTitle>
            <AlertDescription v-if="failure.code === 'email_taken'">This address has an account already. <RouterLink :to="loginTo" class="underline">Log in</RouterLink> and join from here.</AlertDescription>
            <AlertDescription v-else-if="failure.hint">{{ failure.hint }}</AlertDescription>
          </Alert>
          <FormField v-slot="{ componentField }" name="name">
            <FormItem>
              <FormLabel>Your name</FormLabel>
              <FormControl><Input autocomplete="name" v-bind="componentField" /></FormControl>
              <FormMessage />
            </FormItem>
          </FormField>
          <FormField v-slot="{ componentField }" name="email">
            <FormItem>
              <FormLabel>Email</FormLabel>
              <FormControl><Input type="email" autocomplete="username" :readonly="!!lockedEmail" v-bind="componentField" /></FormControl>
              <FormDescription v-if="lockedEmail">The invite is for this address.</FormDescription>
              <FormMessage />
            </FormItem>
          </FormField>
          <FormField v-slot="{ componentField }" name="password">
            <FormItem>
              <FormLabel>Password</FormLabel>
              <FormControl><Input type="password" autocomplete="new-password" v-bind="componentField" /></FormControl>
              <FormDescription>At least 10 characters.</FormDescription>
              <FormMessage />
            </FormItem>
          </FormField>
          <FormField v-slot="{ componentField }" name="confirm">
            <FormItem>
              <FormLabel>Password again</FormLabel>
              <FormControl><Input type="password" autocomplete="new-password" v-bind="componentField" /></FormControl>
              <FormMessage />
            </FormItem>
          </FormField>
          <Button type="submit" variant="secondary" :disabled="busy" class="w-full">{{ busy ? 'Creating...' : 'Create account and join' }}</Button>
        </form>
      </template>
    </div>
  </AuthShell>
</template>
