<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import AuthShell from '@/components/AuthShell.vue'
import { Button } from '@/components/ui/button'
import { FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { api, ApiError } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { SESSION_KEY } from '@/composables/useSession'

const route = useRoute()
const router = useRouter()
const failure = ref<ApiError | null>(null)
const busy = ref(false)

const { handleSubmit } = useForm({
  validationSchema: toTypedSchema(
    z.object({
      email: z.string().min(1, 'Enter your email address.').email('That does not look like an email address.'),
      password: z.string().min(1, 'Enter your password.'),
    }),
  ),
})

const submit = handleSubmit(async (values) => {
  busy.value = true
  failure.value = null
  try {
    await api.login(values.email.trim(), values.password)
    await queryClient.fetchQuery({ queryKey: SESSION_KEY, queryFn: api.session, staleTime: 0 })
    const next = typeof route.query.next === 'string' && route.query.next.startsWith('/') ? route.query.next : '/'
    await router.replace(next)
  } catch (e) {
    failure.value = e instanceof ApiError ? e : new ApiError(0, { message: 'Could not log in.' })
  } finally {
    busy.value = false
  }
})
</script>

<template>
  <AuthShell title="Log in" description="Welcome back. Log in to see your apps.">
    <form class="grid gap-4" novalidate @submit.prevent="submit">
      <Alert v-if="failure" variant="destructive">
        <AlertTitle>{{ failure.message }}</AlertTitle>
        <AlertDescription v-if="failure.hint">{{ failure.hint }}</AlertDescription>
      </Alert>
      <FormField v-slot="{ componentField }" name="email">
        <FormItem>
          <FormLabel>Email</FormLabel>
          <FormControl>
            <Input type="email" autocomplete="username" autofocus v-bind="componentField" />
          </FormControl>
          <FormMessage />
        </FormItem>
      </FormField>
      <FormField v-slot="{ componentField }" name="password">
        <FormItem>
          <FormLabel>Password</FormLabel>
          <FormControl>
            <Input type="password" autocomplete="current-password" v-bind="componentField" />
          </FormControl>
          <FormMessage />
        </FormItem>
      </FormField>
      <Button type="submit" :disabled="busy" class="w-full">{{ busy ? 'Logging in...' : 'Log in' }}</Button>
      <p class="text-center text-xs text-muted-foreground">No account yet? Ask an admin for an invite link.</p>
    </form>
  </AuthShell>
</template>
