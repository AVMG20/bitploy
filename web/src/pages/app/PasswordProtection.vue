<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Eye, EyeOff } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import type { App } from '@/lib/types'

const props = defineProps<{ app: App }>()
const qc = useQueryClient()

const isProtected = computed(() => props.app.protected)
// The form can be open before anything is saved: the app stays public until
// Turn on. A button opens it, so nothing looks on while it is not.
const wantOn = ref(props.app.protected)
const showPassword = ref(false)

const schema = computed(() =>
  toTypedSchema(
    z.object({
      username: z
        .string()
        .trim()
        .min(1, 'Choose a username.')
        .max(64, 'Use at most 64 characters.')
        .regex(/^[A-Za-z0-9._@+-]+$/, 'Use letters, numbers and . _ - @ + only.'),
      password: z
        .string()
        // Count characters like the server does (an emoji is one, not two).
        .refine((p) => (isProtected.value && p === '') || [...p].length >= 8, 'Use at least 8 characters.')
        .refine((p) => new TextEncoder().encode(p).length <= 72, 'Use at most 72 characters.'),
    }),
  ),
)
const form = useForm({
  validationSchema: schema,
  initialValues: { username: props.app.protected_username ?? '', password: '' },
})

let loadedFor = ''
watch(
  () => [props.app.id, props.app.protected, props.app.protected_username] as const,
  ([id, on, name]) => {
    // Follow the saved state, but never wipe what someone is typing during a poll.
    if (loadedFor !== `${id}:${on}:${name}`) {
      loadedFor = `${id}:${on}:${name}`
      wantOn.value = on
      form.resetForm({ values: { username: name ?? '', password: '' } })
      showPassword.value = false
    }
  },
  { immediate: true },
)

const refresh = () => {
  void qc.invalidateQueries({ queryKey: keys.app(props.app.slug) })
  void qc.invalidateQueries({ queryKey: keys.apps })
}

const save = useMutation({
  mutationFn: (v: { username: string; password: string }) =>
    api.setPasswordProtection(props.app.slug, { enabled: true, username: v.username, password: v.password || undefined }),
  onSuccess: (updated, v) => {
    const wasOn = isProtected.value
    qc.setQueryData(keys.app(updated.slug), updated)
    form.resetForm({ values: { username: updated.protected_username ?? v.username, password: '' } })
    showPassword.value = false
    refresh()
    toastOk(wasOn ? 'Login changed' : 'Password protection is on', wasOn ? undefined : 'Visitors now need the username and password.')
  },
  onError: (e) => toastError(e),
})
const submit = form.handleSubmit((v) => save.mutate({ username: v.username.trim(), password: v.password }))

const disable = useMutation({
  mutationFn: () => api.setPasswordProtection(props.app.slug, { enabled: false }),
  onSuccess: (updated) => {
    confirmOff.value = false
    qc.setQueryData(keys.app(updated.slug), updated)
    refresh()
    toastOk('Password protection is off', 'Anyone with the address can open the app again.')
  },
  onError: (e) => toastError(e),
})

// Turning it off makes the app public at once and forgets the login, so ask first.
const confirmOff = ref(false)
function toggle(on: boolean) {
  if (!on && isProtected.value) {
    confirmOff.value = true
    return
  }
  wantOn.value = on
  if (!on) form.resetForm({ values: { username: '', password: '' } })
}

const worker = computed(() => props.app.type !== 'web')
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Password protection</CardTitle>
      <CardDescription>Ask visitors for a username and password before they see the app.</CardDescription>
    </CardHeader>
    <CardContent class="grid gap-4">
      <p v-if="worker" class="text-sm text-muted-foreground">A worker has no web address, so there is nothing to protect.</p>
      <template v-else>
        <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2" data-testid="password-state">
          <div class="min-w-0 text-sm">
            <p class="font-medium">{{ isProtected ? 'On' : wantOn ? 'Not on yet' : 'Off' }}</p>
            <p class="text-xs text-muted-foreground">
              {{ isProtected ? `Visitors sign in as ${app.protected_username}.` : wantOn ? 'Choose a username and password, then press Turn on. Until then anyone with the address can open the app.' : 'Anyone with the address can open the app.' }}
            </p>
          </div>
          <Button v-if="isProtected" variant="outline" size="sm" :disabled="disable.isPending.value || save.isPending.value" @click="toggle(false)">Turn off</Button>
          <Button v-else-if="wantOn" variant="ghost" size="sm" :disabled="save.isPending.value" @click="toggle(false)">Cancel</Button>
          <Button v-else variant="outline" size="sm" @click="toggle(true)">Set a password</Button>
        </div>

        <form v-if="wantOn" class="grid gap-4" novalidate @submit.prevent="submit">
          <div class="grid gap-4 sm:grid-cols-2 sm:items-start">
            <FormField v-slot="{ componentField }" name="username">
              <FormItem>
                <FormLabel>Username</FormLabel>
                <FormControl><Input placeholder="team" autocapitalize="off" autocomplete="off" spellcheck="false" v-bind="componentField" /></FormControl>
                <FormMessage />
              </FormItem>
            </FormField>
            <FormField v-slot="{ componentField }" name="password">
              <FormItem>
                <FormLabel>{{ isProtected ? 'New password' : 'Password' }}</FormLabel>
                <div class="relative">
                  <FormControl>
                    <Input
                      :type="showPassword ? 'text' : 'password'"
                      :placeholder="isProtected ? 'Leave empty to keep it' : 'At least 8 characters'"
                      autocapitalize="off"
                      autocomplete="new-password"
                      spellcheck="false"
                      class="pr-9"
                      v-bind="componentField"
                    />
                  </FormControl>
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    class="absolute top-0 right-0 text-muted-foreground"
                    :aria-label="showPassword ? 'Hide password' : 'Show password'"
                    :aria-pressed="showPassword"
                    @click="showPassword = !showPassword"
                  >
                    <EyeOff v-if="showPassword" />
                    <Eye v-else />
                  </Button>
                </div>
                <FormMessage />
              </FormItem>
            </FormField>
          </div>
          <p class="text-sm text-muted-foreground">
            Covers the app's address and its own domains. The password cannot be shown again after saving, so keep it somewhere safe.
          </p>
          <div>
            <Button type="submit" :disabled="save.isPending.value || (isProtected && !form.meta.value.dirty)">
              {{ save.isPending.value ? 'Saving...' : isProtected ? 'Save changes' : 'Turn on' }}
            </Button>
          </div>
        </form>
      </template>
    </CardContent>
    <ConfirmDialog
      v-model:open="confirmOff"
      title="Turn off password protection?"
      description="Anyone with the address can open the app again. The login is removed, so turning it back on needs a new password."
      confirm-label="Turn off"
      :loading="disable.isPending.value"
      @confirm="disable.mutate()"
    />
  </Card>
</template>
