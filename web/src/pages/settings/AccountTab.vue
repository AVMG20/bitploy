<script setup lang="ts">
import { computed, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import FormScope from '@/components/FormScope.vue'
import SettingsSection from '@/components/SettingsSection.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardFooter } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { keys } from '@/composables/queries'
import { SESSION_KEY, useSession } from '@/composables/useSession'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import type { SessionInfo } from '@/lib/types'

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
        name: z.string().trim().min(1, 'Enter your name.'),
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
    toastOk('Password changed', 'You were logged out on your other devices.')
    pw.resetForm()
    void qc.invalidateQueries({ queryKey: keys.sessions })
  },
  onError: (e) => toastError(e),
})
const submitPw = pw.handleSubmit((v) => savePw.mutate(v))
</script>

<template>
  <div class="grid gap-10">
    <SettingsSection title="Profile">
      <FormScope :form="profile">
        <form novalidate @submit.prevent="submitProfile">
          <Card>
            <CardContent class="grid gap-4 sm:grid-cols-2 sm:items-start">
              <FormField v-slot="{ componentField }" name="name">
                <FormItem><FormLabel>Name</FormLabel><FormControl><Input autocomplete="name" v-bind="componentField" /></FormControl><FormMessage /></FormItem>
              </FormField>
              <FormField v-slot="{ componentField }" name="email">
                <FormItem><FormLabel>Email</FormLabel><FormControl><Input type="email" autocomplete="email" v-bind="componentField" /></FormControl><FormMessage /></FormItem>
              </FormField>
              <FormField v-if="needsPassword" v-slot="{ componentField }" name="password">
                <FormItem class="sm:col-span-2">
                  <FormLabel>Current password</FormLabel>
                  <FormControl><Input type="password" autocomplete="current-password" class="sm:max-w-[calc(50%-0.5rem)]" v-bind="componentField" /></FormControl>
                  <FormDescription>Needed to change your email address.</FormDescription>
                  <FormMessage />
                </FormItem>
              </FormField>
            </CardContent>
            <CardFooter class="justify-end">
              <Button type="submit" :disabled="saveProfile.isPending.value">{{ saveProfile.isPending.value ? 'Saving...' : 'Save profile' }}</Button>
            </CardFooter>
          </Card>
        </form>
      </FormScope>
    </SettingsSection>

    <SettingsSection title="Password" description="Changing it logs you out on all your other devices. API tokens keep working.">
      <FormScope :form="pw">
        <form novalidate @submit.prevent="submitPw">
          <Card>
            <CardContent class="grid gap-4 sm:grid-cols-2 sm:items-start">
              <FormField v-slot="{ componentField }" name="current">
                <FormItem class="sm:col-span-2">
                  <FormLabel>Current password</FormLabel>
                  <FormControl><Input type="password" autocomplete="current-password" class="sm:max-w-[calc(50%-0.5rem)]" v-bind="componentField" /></FormControl>
                  <FormMessage />
                </FormItem>
              </FormField>
              <FormField v-slot="{ componentField }" name="next">
                <FormItem><FormLabel>New password</FormLabel><FormControl><Input type="password" autocomplete="new-password" v-bind="componentField" /></FormControl><FormMessage /></FormItem>
              </FormField>
              <FormField v-slot="{ componentField }" name="confirm">
                <FormItem><FormLabel>New password again</FormLabel><FormControl><Input type="password" autocomplete="new-password" v-bind="componentField" /></FormControl><FormMessage /></FormItem>
              </FormField>
            </CardContent>
            <CardFooter class="justify-between gap-3">
              <p class="text-xs text-muted-foreground">At least 10 characters.</p>
              <Button type="submit" :disabled="savePw.isPending.value">{{ savePw.isPending.value ? 'Changing...' : 'Change password' }}</Button>
            </CardFooter>
          </Card>
        </form>
      </FormScope>
    </SettingsSection>
  </div>
</template>
