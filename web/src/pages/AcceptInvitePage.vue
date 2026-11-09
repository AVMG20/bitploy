<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import AuthShell from '@/components/AuthShell.vue'
import { Button } from '@/components/ui/button'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { api, ApiError } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { SESSION_KEY } from '@/composables/useSession'
import { useSignupRules } from '@/composables/queries'
import { toastOk } from '@/lib/notify'

const route = useRoute()
const router = useRouter()
const failure = ref<ApiError | null>(null)
const busy = ref(false)
// /invite/open is open signup: it works only while Admin > Signups lets anyone make an account.
const open = computed(() => String(route.params.code) === 'open')
const rules = useSignupRules()
const closed = computed(() => open.value && rules.data.value?.open_signup === false)
const description = computed(() => (open.value ? 'Make an account on this bitploy server.' : 'You were invited to bitploy. Pick a password to get started.'))

const { handleSubmit } = useForm({
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

const submit = handleSubmit(async (values) => {
  if (busy.value) return
  busy.value = true
  failure.value = null
  try {
    await api.acceptInvite(String(route.params.code), {
      name: values.name.trim(),
      email: values.email.trim(),
      password: values.password,
    })
    toastOk('Welcome to bitploy', 'Your account is ready.')
    try {
      // The backend may already have logged us in; if not, log in with the new password.
      await queryClient.fetchQuery({ queryKey: SESSION_KEY, queryFn: api.session, staleTime: 0 })
    } catch {
      await api.login(values.email.trim(), values.password)
      await queryClient.fetchQuery({ queryKey: SESSION_KEY, queryFn: api.session, staleTime: 0 })
    }
    await router.replace('/connect')
  } catch (e) {
    failure.value = e instanceof ApiError ? e : new ApiError(0, { message: 'Could not create your account.' })
  } finally {
    busy.value = false
  }
})
</script>

<template>
  <AuthShell title="Create your account" :description="description">
    <div v-if="closed" class="grid gap-4">
      <Alert>
        <AlertTitle>This server does not let people make an account on their own</AlertTitle>
        <AlertDescription>Ask an admin of this server for an invite link.</AlertDescription>
      </Alert>
      <Button variant="outline" as-child class="w-full"><RouterLink to="/login">Log in</RouterLink></Button>
    </div>
    <form v-else class="grid gap-4" novalidate @submit.prevent="submit">
      <Alert v-if="failure" variant="destructive">
        <AlertTitle>{{ failure.message }}</AlertTitle>
        <AlertDescription v-if="failure.hint">{{ failure.hint }}</AlertDescription>
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
          <FormControl><Input type="email" autocomplete="username" v-bind="componentField" /></FormControl>
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
      <Button type="submit" :disabled="busy" class="w-full">{{ busy ? 'Creating...' : 'Create account' }}</Button>
    </form>
  </AuthShell>
</template>
