<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Plus, RefreshCw, Trash2 } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyButton from '@/components/CopyButton.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { FormControl, FormDescription, FormField, FormItem, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { keys, REFRESH_MS, useApp } from '@/composables/queries'
import { api } from '@/lib/api'
import { domainStatusInfo, hostOf } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import type { Domain } from '@/lib/types'

const props = defineProps<{ slug: string }>()
const qc = useQueryClient()
const app = useApp(() => props.slug)
const domains = useQuery({
  queryKey: computed(() => keys.domains(props.slug)),
  queryFn: () => api.domains(props.slug),
  refetchInterval: REFRESH_MS * 3,
})

function refresh() {
  void qc.invalidateQueries({ queryKey: keys.domains(props.slug) })
  void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
}

const { handleSubmit, resetForm } = useForm({
  validationSchema: toTypedSchema(
    z.object({
      hostname: z
        .string()
        .trim()
        .min(1, 'Type the web address you want to use.')
        .transform((v) => v.toLowerCase().replace(/^https?:\/\//, '').replace(/\/.*$/, ''))
        .refine((v) => /^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$/.test(v), 'Use a full address like www.example.com (no https://).'),
    }),
  ),
})

const add = useMutation({
  mutationFn: (hostname: string) => api.addDomain(props.slug, hostname),
  onSuccess: (d) => {
    toastOk(`Added ${d.hostname}`, 'Now add the DNS records shown below.')
    resetForm()
    refresh()
  },
  onError: (e) => toastError(e),
})
const submit = handleSubmit((v) => add.mutate(v.hostname))

const checking = ref<string | null>(null)
const check = useMutation({
  mutationFn: (host: string) => {
    checking.value = host
    return api.checkDomain(props.slug, host)
  },
  onSuccess: (d) => {
    const info = domainStatusInfo(d.status)
    if (d.status === 'active') toastOk(`${d.hostname} is working`)
    else toastOk(`${d.hostname}: ${info.label}`, d.last_error || 'Not ready yet. DNS changes can take a while.')
    refresh()
  },
  onError: (e) => toastError(e),
  onSettled: () => (checking.value = null),
})

const toRemove = ref<Domain | null>(null)
const removeOpen = computed({
  get: () => toRemove.value !== null,
  set: (v) => {
    if (!v) toRemove.value = null
  },
})
const remove = useMutation({
  mutationFn: (host: string) => api.removeDomain(props.slug, host),
  onSuccess: () => {
    toastOk('Domain removed')
    toRemove.value = null
    refresh()
  },
  onError: (e) => toastError(e),
})
</script>

<template>
  <div class="grid gap-4">
    <Card>
      <CardHeader>
        <CardTitle>Free address</CardTitle>
        <CardDescription>Always works, also after you add your own domain.</CardDescription>
      </CardHeader>
      <CardContent v-if="app.data.value">
        <div class="flex items-center gap-1 rounded-lg border bg-muted/40 py-1 pr-1 pl-3">
          <a :href="app.data.value.url" target="_blank" rel="noopener noreferrer" class="min-w-0 flex-1 truncate font-mono text-sm hover:underline">
            {{ hostOf(app.data.value.url) }}
          </a>
          <CopyButton :value="app.data.value.url" what="Link copied" label="Copy link" />
        </div>
      </CardContent>
      <CardContent v-else><Skeleton class="h-10 w-full" /></CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle>Your own domain</CardTitle>
        <CardDescription>Use a domain you own, like www.example.com. You add a few DNS records where you bought it.</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="flex flex-col gap-2 sm:flex-row sm:items-start" novalidate @submit.prevent="submit">
          <FormField v-slot="{ componentField }" name="hostname">
            <FormItem class="flex-1">
              <FormControl><Input placeholder="www.example.com" autocapitalize="off" spellcheck="false" aria-label="Domain name" v-bind="componentField" /></FormControl>
              <FormDescription>Only the address, without https://</FormDescription>
              <FormMessage />
            </FormItem>
          </FormField>
          <Button type="submit" :disabled="add.isPending.value"><Plus /> {{ add.isPending.value ? 'Adding...' : 'Add domain' }}</Button>
        </form>
      </CardContent>
    </Card>

    <QueryError v-if="domains.isError.value" :error="domains.error.value" @retry="domains.refetch()" />
    <Skeleton v-else-if="domains.isPending.value" class="h-32 w-full" />

    <Card v-for="d in domains.data.value ?? []" :key="d.id">
      <CardHeader>
        <div class="flex flex-wrap items-center gap-2">
          <CardTitle class="font-mono text-base break-all">{{ d.hostname }}</CardTitle>
          <StatusBadge v-bind="domainStatusInfo(d.status)" />
          <div class="ml-auto flex gap-2">
            <Button size="sm" variant="outline" :disabled="check.isPending.value" :aria-label="`Check ${d.hostname} now`" @click="check.mutate(d.hostname)">
              <RefreshCw :class="checking === d.hostname && 'animate-spin'" /> Check now
            </Button>
            <Button size="sm" variant="destructive" :aria-label="`Remove ${d.hostname}`" @click="toRemove = d"><Trash2 /> Remove</Button>
          </div>
        </div>
        <CardDescription>
          Last checked <RelativeTime :date="d.last_checked_at" empty="not yet" />
        </CardDescription>
      </CardHeader>
      <CardContent class="grid gap-3">
        <p v-if="d.last_error" class="rounded-lg bg-destructive/10 p-3 text-sm text-destructive">{{ d.last_error }}</p>
        <template v-if="d.status !== 'active' && d.records?.length">
          <p class="text-sm">Add these records where you bought the domain. We check every 5 minutes; changes can take a few hours to show up.</p>
          <div class="overflow-x-auto rounded-lg border bg-card">
            <table class="w-full text-sm">
              <thead class="bg-muted/50 text-left text-xs text-muted-foreground">
                <tr><th class="px-3 py-2">Type</th><th class="px-3 py-2">Name</th><th class="px-3 py-2">Value</th></tr>
              </thead>
              <tbody>
                <tr v-for="(r, i) in d.records" :key="i" class="border-t align-top">
                  <td class="px-3 py-2 font-mono">{{ r.type }}</td>
                  <td class="px-3 py-2"><div class="flex items-center gap-1"><span class="font-mono break-all">{{ r.name }}</span><CopyButton :value="r.name" what="Name copied" /></div></td>
                  <td class="px-3 py-2"><div class="flex items-center gap-1"><span class="font-mono break-all">{{ r.value }}</span><CopyButton :value="r.value" what="Value copied" /></div></td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>
        <ul v-if="d.notes?.length" class="list-disc pl-5 text-sm text-muted-foreground">
          <li v-for="(n, i) in d.notes" :key="i">{{ n }}</li>
        </ul>
        <p v-if="d.status === 'active'" class="text-sm text-muted-foreground">All set. <a :href="`https://${d.hostname}`" target="_blank" rel="noopener noreferrer" class="underline">Open {{ d.hostname }}</a></p>
      </CardContent>
    </Card>

    <ConfirmDialog
      v-model:open="removeOpen"
      title="Remove this domain?"
      :description="`${toRemove?.hostname ?? 'This domain'} will stop showing your app. Your free address keeps working.`"
      confirm-label="Remove domain"
      :loading="remove.isPending.value"
      @confirm="toRemove && remove.mutate(toRemove.hostname)"
    />
  </div>
</template>
