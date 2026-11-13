<script setup lang="ts">
// Who can make an account here, and the limits new accounts get. Saved on
// the server (GET/PATCH /admin/settings) and read on every signup, so a change
// applies to the next account. Accounts that exist keep their limits.
import { computed, ref, watch } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { formatMB } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import { formToLimits, limitsOverServer, limitsToForm, SIGNUP_MODES, type LimitsForm } from '@/lib/signups'
import type { ServerSettings, SignupMode } from '@/lib/types'
import { cn } from '@/lib/utils'

const qc = useQueryClient()
const settings = useQuery({ queryKey: keys.adminSettings, queryFn: api.adminSettings })
const st = computed(() => settings.data.value)

// ---- who can make an account ----
const mode = ref<SignupMode>('any_invite')
const ownApps = ref(true)
const accessDirty = computed(() => !!st.value && (mode.value !== st.value.signups || ownApps.value !== st.value.project_invite_apps))

function saved(s: ServerSettings) {
  qc.setQueryData(keys.adminSettings, s)
  void qc.invalidateQueries({ queryKey: keys.signupRules })
}

const saveAccess = useMutation({
  mutationFn: () => api.updateAdminSettings({ signups: mode.value, project_invite_apps: ownApps.value }),
  onSuccess: (s) => {
    saved(s)
    fillAccess(s)
    toastOk('Signup settings saved')
  },
  onError: (e) => toastError(e),
})

// ---- limits for new accounts ----
const whole = (label: string, min = 0) =>
  z.coerce.number({ invalid_type_error: `${label}: enter a number.` }).int(`${label}: use a whole number.`).min(min, `${label}: at least ${min}.`)
const form = useForm({
  validationSchema: toTypedSchema(
    z
      .object({
        apps: whole('Apps', 0),
        memory_gb: z.coerce.number({ invalid_type_error: 'Memory: enter a number.' }).min(0, 'Memory: at least 0.'),
        cpus: z.coerce.number({ invalid_type_error: 'CPU cores: enter a number.' }).min(0, 'CPU cores: at least 0.'),
        domains_per_app: whole('Domains', 0),
        volume_gb: z.coerce.number({ invalid_type_error: 'Storage: enter a number.' }).min(0.1, 'Storage: at least 0.1 GB.'),
      })
      .superRefine((v, ctx) => {
        const server = st.value?.server
        if (!server) return
        for (const [path, message] of Object.entries(limitsOverServer(formToLimits(v), server))) {
          ctx.addIssue({ code: z.ZodIssueCode.custom, path: [path], message })
        }
      }),
  ),
})

const fields = [
  { name: 'apps', label: 'Apps', hint: '0: no apps of their own; they can still work in projects shared with them.' },
  { name: 'domains_per_app', label: 'Custom domains per app', hint: '0: none, only the bitploy address.' },
  { name: 'volume_gb', label: 'Storage in total (GB)', hint: 'disk' },
  { name: 'memory_gb', label: 'Memory in total (GB)', hint: 'memory' },
  { name: 'cpus', label: 'CPU cores in total', hint: 'cpu' },
] as const

/** What the server has, under the field it limits. */
function hint(h: string): string {
  const server = st.value?.server
  if (h === 'memory') return `${server?.memory_mb ? `This server has ${formatMB(server.memory_mb)}. ` : ''}0: no apps of their own.`
  if (h === 'cpu') return `${server?.cpu_millis ? `This server has ${server.cpu_millis / 1000} cores. ` : ''}0.5 is half of one core; 0: no apps of their own.`
  if (h === 'disk') return server?.disk_mb ? `The disk holds ${formatMB(server.disk_mb)}.` : ''
  return h
}

const limitsDirty = computed(() => {
  if (!st.value) return false
  const now = formToLimits(form.values as LimitsForm)
  return (Object.keys(now) as (keyof typeof now)[]).some((k) => now[k] !== st.value!.new_account_quota[k])
})

// Fill both forms from the server once; a save refills only its own form, so
// unsaved changes in the other one stay.
function fillAccess(s: ServerSettings) {
  mode.value = s.signups
  ownApps.value = s.project_invite_apps
}
const fillLimits = (s: ServerSettings) => form.resetForm({ values: limitsToForm(s.new_account_quota) })
const filled = ref(false)
watch(
  st,
  (s) => {
    if (!s || filled.value) return
    filled.value = true
    fillAccess(s)
    fillLimits(s)
  },
  { immediate: true },
)

const saveLimits = useMutation({
  mutationFn: (v: LimitsForm) => api.updateAdminSettings({ new_account_quota: formToLimits(v) }),
  onSuccess: (s) => {
    saved(s)
    fillLimits(s)
    toastOk('Limits saved')
  },
  onError: (e) => toastError(e),
})
const submitLimits = form.handleSubmit((v) => {
  if (!saveLimits.isPending.value) saveLimits.mutate(v)
})
</script>

<template>
  <div class="grid gap-4">
    <QueryError v-if="settings.isError.value" :error="settings.error.value" @retry="settings.refetch()" />
    <template v-else-if="!st">
      <Skeleton class="h-64 w-full" />
      <Skeleton class="h-64 w-full" />
    </template>
    <template v-else>
      <Card>
        <CardHeader>
          <CardTitle>Who can create an account</CardTitle>
          <CardDescription>Project owners can always invite people who already have an account here. This decides who can make a new account.</CardDescription>
        </CardHeader>
        <CardContent class="grid gap-4">
          <div class="grid gap-2" role="radiogroup" aria-label="Who can create an account">
            <button
              v-for="m in SIGNUP_MODES"
              :key="m.value"
              type="button"
              role="radio"
              :aria-checked="mode === m.value"
              :class="cn('flex items-start gap-3 rounded-lg border p-3 text-left transition-colors hover:bg-muted/50', mode === m.value && 'border-primary bg-primary/5')"
              @click="mode = m.value"
            >
              <span :class="cn('mt-0.5 grid size-4 shrink-0 place-items-center rounded-full border', mode === m.value ? 'border-primary' : 'border-muted-foreground/50')">
                <span v-if="mode === m.value" class="size-2 rounded-full bg-primary" />
              </span>
              <span class="grid gap-0.5">
                <span class="text-sm font-medium">{{ m.label }}<Badge v-if="m.value === 'any_invite'" variant="secondary" class="ml-2">Default</Badge></span>
                <span class="text-xs text-muted-foreground">{{ m.blurb }}</span>
              </span>
            </button>
          </div>
          <div v-if="mode !== 'admin_invites'" class="flex items-start justify-between gap-3 rounded-lg border p-3">
            <div class="grid gap-1">
              <Label for="own-apps">Accounts made with a project invite can deploy apps of their own</Label>
              <p class="text-sm text-muted-foreground">
                <template v-if="ownApps">They get the limits below.</template>
                <template v-else>They get no apps, memory or CPU of their own and work in the project they were invited to, on its owner's limits. Raise them for one person with Limits in People.</template>
              </p>
            </div>
            <Switch id="own-apps" v-model="ownApps" />
          </div>
        </CardContent>
        <CardFooter class="flex-wrap justify-between gap-3">
          <p class="text-xs text-muted-foreground"><template v-if="st.updated_at">Last changed <RelativeTime :date="st.updated_at" />.</template></p>
          <Button :disabled="!accessDirty || saveAccess.isPending.value" @click="saveAccess.mutate()">{{ saveAccess.isPending.value ? 'Saving...' : 'Save' }}</Button>
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Limits for new accounts</CardTitle>
          <CardDescription>Every account made from now on gets these. Accounts that exist keep theirs: change those with Limits in People.</CardDescription>
        </CardHeader>
        <CardContent>
          <form id="new-account-limits" class="grid items-start gap-3 sm:grid-cols-2" novalidate @submit.prevent="submitLimits">
            <FormField v-for="f in fields" :key="f.name" v-slot="{ componentField }" :name="f.name">
              <FormItem>
                <FormLabel>{{ f.label }}</FormLabel>
                <FormControl><Input type="number" step="any" inputmode="decimal" v-bind="componentField" /></FormControl>
                <FormDescription v-if="hint(f.hint)">{{ hint(f.hint) }}</FormDescription>
                <FormMessage />
              </FormItem>
            </FormField>
          </form>
        </CardContent>
        <CardFooter class="justify-end">
          <Button type="submit" form="new-account-limits" :disabled="!limitsDirty || saveLimits.isPending.value">{{ saveLimits.isPending.value ? 'Saving...' : 'Save limits' }}</Button>
        </CardFooter>
      </Card>
    </template>
  </div>
</template>
