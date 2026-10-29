<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { KeyRound, Pencil, Trash2 } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { keys, useApp, useMe } from '@/composables/queries'
import { api } from '@/lib/api'
import { formatMB } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import { queryClient } from '@/lib/queryClient'

const props = defineProps<{ slug: string }>()
const router = useRouter()
const qc = useQueryClient()
const app = useApp(() => props.slug)
const me = useMe()
const quota = computed(() => me.data.value?.quota)

// ---------------------------------------------------------------- env vars
const env = useQuery({
  queryKey: computed(() => keys.env(props.slug)),
  queryFn: () => api.env(props.slug),
})

const envForm = useForm({
  validationSchema: toTypedSchema(
    z.object({
      name: z
        .string()
        .trim()
        .min(1, 'Give the variable a name.')
        .regex(/^[A-Za-z_][A-Za-z0-9_]*$/, 'Use letters, numbers and underscores only, like API_KEY.'),
      value: z.string(),
    }),
  ),
  initialValues: { name: '', value: '' },
})

const saveEnv = useMutation({
  mutationFn: (v: { name: string; value: string }) => api.setEnv(props.slug, { [v.name]: v.value }, []),
  onSuccess: (_r, v) => {
    toastOk(`Saved ${v.name}`, 'The new value is used the next time the app starts.')
    envForm.resetForm({ values: { name: '', value: '' } })
    void qc.invalidateQueries({ queryKey: keys.env(props.slug) })
  },
  onError: (e) => toastError(e),
})
const submitEnv = envForm.handleSubmit((v) => saveEnv.mutate({ name: v.name.trim(), value: v.value }))

function editEnv(name: string) {
  envForm.resetForm({ values: { name, value: '' } })
  document.getElementById('env-value')?.focus()
}

const envToRemove = ref<string | null>(null)
const envRemoveOpen = computed({
  get: () => envToRemove.value !== null,
  set: (v) => {
    if (!v) envToRemove.value = null
  },
})
const removeEnv = useMutation({
  mutationFn: (name: string) => api.setEnv(props.slug, {}, [name]),
  onSuccess: (_r, name) => {
    toastOk(`Removed ${name}`)
    envToRemove.value = null
    void qc.invalidateQueries({ queryKey: keys.env(props.slug) })
  },
  onError: (e) => toastError(e),
})

// ---------------------------------------------------------------- resources
const resForm = useForm({
  validationSchema: computed(() =>
    toTypedSchema(
      z.object({
        volume: z.boolean(),
        memory_mb: z.coerce
          .number({ invalid_type_error: 'Enter a number.' })
          .int('Use a whole number.')
          .min(64, 'At least 64 MB.')
          .max(quota.value?.memory_mb || 100000, `Your plan allows up to ${quota.value?.memory_mb ?? ''} MB.`),
        cpus: z.coerce
          .number({ invalid_type_error: 'Enter a number.' })
          .min(0.1, 'At least 0.1.')
          .max((quota.value?.cpu_millis || 100000) / 1000, `Your plan allows up to ${(quota.value?.cpu_millis ?? 0) / 1000}.`),
      }),
    ),
  ),
  initialValues: { volume: false, memory_mb: 512, cpus: 0.5 },
})

let resLoaded = false
watch(
  () => app.data.value,
  (a) => {
    if (a && !resLoaded) {
      resLoaded = true
      resForm.resetForm({ values: { volume: a.volume, memory_mb: a.memory_mb, cpus: a.cpu_millis / 1000 } })
    }
  },
  { immediate: true },
)

const saveRes = useMutation({
  mutationFn: (v: { volume: boolean; memory_mb: number; cpus: number }) =>
    api.updateApp(props.slug, { volume: v.volume, memory_mb: v.memory_mb, cpu_millis: Math.round(v.cpus * 1000) }),
  onSuccess: () => {
    toastOk('Settings saved', 'Changes apply the next time the app starts.')
    void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.me })
  },
  onError: (e) => toastError(e),
})
const submitRes = resForm.handleSubmit((v) => saveRes.mutate(v))

// ---------------------------------------------------------------- delete
const deleteOpen = ref(false)
const del = useMutation({
  mutationFn: () => api.deleteApp(props.slug),
  onSuccess: async () => {
    toastOk('App deleted')
    deleteOpen.value = false
    queryClient.removeQueries({ queryKey: keys.app(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.apps })
    void qc.invalidateQueries({ queryKey: keys.me })
    await router.replace('/')
  },
  onError: (e) => toastError(e),
})
</script>

<template>
  <div class="grid gap-4">
    <Card>
      <CardHeader>
        <CardTitle>Secrets and settings (environment variables)</CardTitle>
        <CardDescription>
          Passwords, API keys and other values your app needs. For safety, values are never shown again after saving.
        </CardDescription>
      </CardHeader>
      <CardContent class="grid gap-4">
        <QueryError v-if="env.isError.value" :error="env.error.value" @retry="env.refetch()" />
        <Skeleton v-else-if="env.isPending.value" class="h-20 w-full" />
        <EmptyState v-else-if="!env.data.value?.length" :icon="KeyRound" title="No variables yet." description="Add one below. Your app can read it like any environment variable." />
        <ul v-else class="divide-y rounded-lg border">
          <li v-for="v in env.data.value" :key="v.name" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
            <span class="font-mono text-sm font-medium">{{ v.name }}</span>
            <span class="font-mono text-sm text-muted-foreground">{{ v.masked }}</span>
            <span class="text-xs text-muted-foreground">changed <RelativeTime :date="v.updated_at" /></span>
            <div class="ml-auto flex gap-1">
              <Button size="sm" variant="ghost" @click="editEnv(v.name)"><Pencil /> Change</Button>
              <Button size="sm" variant="ghost" class="text-destructive" @click="envToRemove = v.name"><Trash2 /> Remove</Button>
            </div>
          </li>
        </ul>

        <form class="grid gap-3 sm:grid-cols-[1fr_2fr_auto] sm:items-start" novalidate @submit.prevent="submitEnv">
          <FormField v-slot="{ componentField }" name="name">
            <FormItem>
              <FormLabel>Name</FormLabel>
              <FormControl><Input placeholder="API_KEY" autocapitalize="off" spellcheck="false" class="font-mono" v-bind="componentField" /></FormControl>
              <FormMessage />
            </FormItem>
          </FormField>
          <FormField v-slot="{ componentField }" name="value">
            <FormItem>
              <FormLabel>Value</FormLabel>
              <FormControl><Textarea id="env-value" rows="1" placeholder="Paste the secret value" autocomplete="off" spellcheck="false" class="min-h-8 font-mono" v-bind="componentField" /></FormControl>
              <FormMessage />
            </FormItem>
          </FormField>
          <Button type="submit" class="sm:mt-[1.625rem]" :disabled="saveEnv.isPending.value">{{ saveEnv.isPending.value ? 'Saving...' : 'Save variable' }}</Button>
        </form>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle>Storage and size</CardTitle>
        <CardDescription>Decide how much computer power this app gets, and whether it can keep files.</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="grid gap-4" novalidate @submit.prevent="submitRes">
          <FormField v-slot="{ value, handleChange }" name="volume">
            <FormItem class="flex flex-row items-start justify-between gap-4 rounded-lg border p-3">
              <div class="grid gap-1">
                <FormLabel>Keep files between deploys</FormLabel>
                <FormDescription>
                  Turns on a storage folder at /data that survives new versions. While it is on, deploys restart the app for a few seconds instead of switching over smoothly.
                </FormDescription>
              </div>
              <FormControl><Switch :model-value="value" @update:model-value="handleChange" /></FormControl>
            </FormItem>
          </FormField>
          <div class="grid gap-4 sm:grid-cols-2">
            <FormField v-slot="{ componentField }" name="memory_mb">
              <FormItem>
                <FormLabel>Memory (MB)</FormLabel>
                <FormControl><Input type="number" min="64" step="64" inputmode="numeric" v-bind="componentField" /></FormControl>
                <FormDescription v-if="quota">Up to {{ formatMB(quota.memory_mb) }} in total across your apps.</FormDescription>
                <FormMessage />
              </FormItem>
            </FormField>
            <FormField v-slot="{ componentField }" name="cpus">
              <FormItem>
                <FormLabel>Processor (CPUs)</FormLabel>
                <FormControl><Input type="number" min="0.1" step="0.1" inputmode="decimal" v-bind="componentField" /></FormControl>
                <FormDescription v-if="quota">Up to {{ quota.cpu_millis / 1000 }} CPUs in total across your apps.</FormDescription>
                <FormMessage />
              </FormItem>
            </FormField>
          </div>
          <div><Button type="submit" :disabled="saveRes.isPending.value">{{ saveRes.isPending.value ? 'Saving...' : 'Save changes' }}</Button></div>
        </form>
      </CardContent>
    </Card>

    <Card class="border-destructive/40">
      <CardHeader>
        <CardTitle class="text-destructive">Delete this app</CardTitle>
        <CardDescription>This removes the app, its address, its stored files and all old versions. It cannot be undone.</CardDescription>
      </CardHeader>
      <CardContent>
        <Button variant="destructive" @click="deleteOpen = true"><Trash2 /> Delete app</Button>
      </CardContent>
    </Card>

    <ConfirmDialog
      v-model:open="envRemoveOpen"
      title="Remove this variable?"
      :description="`${envToRemove ?? 'The variable'} will be gone the next time the app starts. Your app may stop working if it needs it.`"
      confirm-label="Remove"
      :loading="removeEnv.isPending.value"
      @confirm="envToRemove && removeEnv.mutate(envToRemove)"
    />
    <ConfirmDialog
      v-model:open="deleteOpen"
      title="Delete this app for good?"
      description="Everything about it is removed, including files stored in /data."
      confirm-label="Delete app"
      :type-to-confirm="slug"
      :loading="del.isPending.value"
      @confirm="del.mutate()"
    />
  </div>
</template>
