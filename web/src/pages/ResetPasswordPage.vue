<script setup lang="ts">
import { computed, ref } from 'vue'
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
import { queryClient } from '@/lib/queryClient'
import { toastOk } from '@/lib/notify'

// A password reset link an admin made (/reset/<code>). It shows whose account
// it is for, then sets the new password, signs out every other session and
// logs in. It works once and for 24 hours.
const route = useRoute()
const router = useRouter()
const code = computed(() => String(route.params.code))

const info = useQuery({
  queryKey: computed(() => ['password-reset', code.value]),
  queryFn: () => api.passwordResetInfo(code.value),
  retry: false,
})
/** A link that is wrong, used or expired, or a suspended account (not a hiccup that can be retried). */
const badLink = computed(() => {
  const e = info.error.value
  return e instanceof ApiError && e.status >= 400 && e.status < 500 ? e : null
})

const failure = ref<ApiError | null>(null)
const busy = ref(false)

const { handleSubmit } = useForm({
  initialValues: { password: '', confirm: '' },
  validationSchema: toTypedSchema(
    z
      .object({
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
    const session = await api.usePasswordReset(code.value, values.password)
    // This browser may have been logged in as someone else: start clean.
    queryClient.clear()
    queryClient.setQueryData(SESSION_KEY, session)
    toastOk('Password changed', 'You are logged in. Every other session of your account was signed out.')
    await router.replace('/')
  } catch (e) {
    failure.value = e instanceof ApiError ? e : new ApiError(0, { message: 'Could not set the password.' })
  } finally {
    busy.value = false
  }
})
</script>

<template>
  <AuthShell title="Pick a new password" :description="info.data.value ? `For ${info.data.value.name} (${info.data.value.email}).` : undefined">
    <div class="grid gap-4">
      <div v-if="info.isPending.value" class="grid gap-3">
        <Skeleton class="h-10 w-full" />
        <Skeleton class="h-10 w-full" />
      </div>

      <template v-else-if="info.isError.value">
        <Alert v-if="badLink" variant="destructive">
          <AlertTitle>{{ badLink.message }}</AlertTitle>
          <AlertDescription>{{ badLink.hint || 'Ask an admin of this server for a new link.' }}</AlertDescription>
        </Alert>
        <QueryError v-else :error="info.error.value" @retry="info.refetch()" />
        <Button variant="outline" as-child class="w-full"><RouterLink to="/login">Log in</RouterLink></Button>
      </template>

      <form v-else class="grid gap-4" novalidate @submit.prevent="submit">
        <Alert v-if="failure" variant="destructive">
          <AlertTitle>{{ failure.message }}</AlertTitle>
          <AlertDescription v-if="failure.hint">{{ failure.hint }}</AlertDescription>
        </Alert>
        <!-- Lets a password manager save the new password for the right account. -->
        <input type="email" autocomplete="username" :value="info.data.value?.email" class="sr-only" tabindex="-1" aria-hidden="true" readonly />
        <FormField v-slot="{ componentField }" name="password">
          <FormItem>
            <FormLabel>New password</FormLabel>
            <FormControl><Input type="password" autocomplete="new-password" v-bind="componentField" /></FormControl>
            <FormDescription>At least 10 characters. You are logged out everywhere else; API tokens keep working.</FormDescription>
            <FormMessage />
          </FormItem>
        </FormField>
        <FormField v-slot="{ componentField }" name="confirm">
          <FormItem>
            <FormLabel>New password again</FormLabel>
            <FormControl><Input type="password" autocomplete="new-password" v-bind="componentField" /></FormControl>
            <FormMessage />
          </FormItem>
        </FormField>
        <Button type="submit" :disabled="busy" class="w-full">{{ busy ? 'Saving...' : 'Set password and log in' }}</Button>
      </form>
    </div>
  </AuthShell>
</template>
