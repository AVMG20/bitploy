<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { ArrowLeft, ChevronRight, ExternalLink } from 'lucide-vue-next'
import CatalogMonogram from '@/components/CatalogMonogram.vue'
import CodeBlock from '@/components/CodeBlock.vue'
import DomainSelect from '@/components/DomainSelect.vue'
import PageHeader from '@/components/PageHeader.vue'
import QueryError from '@/components/QueryError.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { keys, useCatalogApp, useMe } from '@/composables/queries'
import { api, ApiError } from '@/lib/api'
import { friendlyError } from '@/lib/errors'
import type { FriendlyError } from '@/lib/errors'
import { formatMB } from '@/lib/format'
import { addressParts as toAddressParts, slugify, slugSchema } from '@/lib/slug'

const props = defineProps<{ catalogId: string }>()
const router = useRouter()
const route = useRoute()
const qc = useQueryClient()
const me = useMe()
const entry = useCatalogApp(() => props.catalogId)
const notFound = computed(() => entry.error.value instanceof ApiError && entry.error.value.status === 404)

// ---------------------------------------------------------------- name and address
const form = useForm({
  validationSchema: toTypedSchema(
    z.object({
      name: z.string().trim().min(1, 'Give the app a name.').max(80, 'Use at most 80 characters.'),
      slug: slugSchema,
    }),
  ),
  initialValues: { name: '', slug: '' },
})

const editingAddress = ref(false)
const addressEdited = ref(false)
let filledFor = ''
watch(
  () => entry.data.value,
  (c) => {
    if (c && filledFor !== c.id) {
      filledFor = c.id
      // A name typed on New app before picking this one wins over the app's own.
      const name = typeof route.query.name === 'string' && route.query.name.trim() ? route.query.name.trim() : c.name
      const slug = typeof route.query.slug === 'string' && route.query.slug ? route.query.slug : ''
      if (slug) addressEdited.value = true
      form.resetForm({ values: { name, slug: slug || slugify(name) } })
    }
  },
  { immediate: true },
)

// The address follows the name until the person edits it.
watch(
  () => form.values.name,
  (name) => {
    if (!addressEdited.value) form.setFieldValue('slug', slugify(name ?? ''), !!form.values.slug)
  },
)
function editAddress() {
  editingAddress.value = true
  addressEdited.value = true
}
const domain = ref('') // "" is the default domain
const addressParts = computed(() => toAddressParts(me.data.value, domain.value))
if (typeof route.query.domain === 'string') domain.value = route.query.domain

// ---------------------------------------------------------------- deploy
const SLUG_CODES = new Set(['slug_invalid', 'slug_reserved', 'slug_taken'])
const error = ref<FriendlyError | null>(null)
const showFile = ref(false)

const deploy = useMutation({
  mutationFn: (v: { name: string; slug: string }) => api.deployCatalog(props.catalogId, { name: v.name, slug: v.slug, domain: domain.value || undefined }),
  onSuccess: async (r) => {
    qc.setQueryData(keys.app(r.app.slug), r.app)
    void qc.invalidateQueries({ queryKey: keys.apps })
    void qc.invalidateQueries({ queryKey: keys.me })
    void qc.invalidateQueries({ queryKey: keys.deployments(r.app.slug) })
    await router.push({ name: 'app', params: { slug: r.app.slug } })
  },
  onError: (e) => {
    const f = friendlyError(e)
    if (SLUG_CODES.has(f.code)) {
      editingAddress.value = true
      addressEdited.value = true
      form.setFieldError('slug', f.hint ? `${f.message} ${f.hint}` : f.message)
    } else {
      error.value = f
    }
  },
})
const submit = form.handleSubmit((v) => {
  error.value = null
  deploy.mutate(v)
})
</script>

<template>
  <div class="grid gap-6">
    <RouterLink :to="{ name: 'new-app', query: { source: 'ready-made' } }" class="inline-flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
      <ArrowLeft class="size-4" /> New project
    </RouterLink>

    <Alert v-if="notFound" variant="destructive">
      <AlertTitle>This ready-made app does not exist.</AlertTitle>
      <AlertDescription><RouterLink :to="{ name: 'new-app', query: { source: 'ready-made' } }" class="underline">Back to New project</RouterLink></AlertDescription>
    </Alert>
    <QueryError v-else-if="entry.isError.value" :error="entry.error.value" @retry="entry.refetch()" />
    <div v-else-if="!entry.data.value" class="grid gap-4">
      <Skeleton class="h-14 w-72" />
      <Skeleton class="h-64 w-full" />
    </div>

    <template v-else>
      <div class="flex items-center gap-3">
        <CatalogMonogram :name="entry.data.value.name" size="lg" />
        <PageHeader :title="entry.data.value.name" :description="entry.data.value.tagline" />
      </div>

      <form novalidate class="grid gap-6" @submit.prevent="submit">
        <Card>
          <CardContent class="grid items-start gap-5 lg:grid-cols-2">
            <FormField v-slot="{ componentField }" name="name">
              <FormItem>
                <FormLabel>Name</FormLabel>
                <FormControl><Input autocomplete="off" :disabled="deploy.isPending.value" v-bind="componentField" /></FormControl>
                <FormMessage />
              </FormItem>
            </FormField>

            <FormField v-slot="{ componentField }" name="slug">
              <FormItem>
                <div class="flex items-center justify-between gap-3">
                  <FormLabel>Address</FormLabel>
                  <Button v-if="!editingAddress" type="button" variant="ghost" size="sm" class="-my-1 h-6 px-2 text-xs" :disabled="deploy.isPending.value" @click="editAddress">Change</Button>
                </div>
                <div v-if="editingAddress" class="flex min-w-0 items-center gap-2">
                  <FormControl><Input autocapitalize="off" autocomplete="off" spellcheck="false" class="min-w-0 font-mono" :disabled="deploy.isPending.value" v-bind="componentField" /></FormControl>
                  <DomainSelect v-model="domain" :disabled="deploy.isPending.value" />
                </div>
                <p class="truncate rounded-lg border bg-muted/30 px-3 py-2 font-mono text-[13px] text-muted-foreground" data-testid="address-preview">
                  {{ addressParts.before }}<span class="font-medium text-foreground">{{ form.values.slug || 'your-app' }}</span>{{ addressParts.after }}
                </p>
                <FormDescription v-if="!editingAddress">This is where people open the app. It follows the name.</FormDescription>
                <FormMessage />
              </FormItem>
            </FormField>
          </CardContent>
        </Card>

        <Card>
          <CardContent class="grid gap-4 text-sm">
            <p class="text-muted-foreground">{{ entry.data.value.description }}</p>
            <dl class="grid grid-cols-[8rem_1fr] gap-x-4 gap-y-2.5">
              <dt class="text-muted-foreground">Version</dt>
              <dd class="font-mono">{{ entry.data.value.version }}</dd>
              <dt class="text-muted-foreground">Memory</dt>
              <dd>{{ formatMB(entry.data.value.memory_mb) }}</dd>
              <dt class="text-muted-foreground">Storage</dt>
              <dd>
                <template v-if="entry.data.value.volume">{{ formatMB(entry.data.value.volume_mb) }} of saved files <span class="font-mono text-xs text-muted-foreground">/data</span></template>
                <template v-else>None. Nothing is kept between deploys.</template>
              </dd>
              <dt class="text-muted-foreground">Website</dt>
              <dd class="min-w-0">
                <a :href="entry.data.value.website" target="_blank" rel="noopener noreferrer" class="inline-flex max-w-full items-center gap-1 hover:underline">
                  <span class="truncate">{{ entry.data.value.website }}</span> <ExternalLink class="size-3.5 shrink-0" />
                </a>
              </dd>
            </dl>

            <div v-if="entry.data.value.env.length" class="grid gap-2">
              <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Environment variables it adds</p>
              <ul class="divide-y rounded-lg border">
                <li v-for="v in entry.data.value.env" :key="v.name" class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 px-3 py-2">
                  <span class="font-mono text-[13px] font-medium">{{ v.name }}</span>
                  <span v-if="v.generated" class="text-xs text-muted-foreground">generated for you</span>
                  <span v-if="v.description" class="basis-full text-xs text-muted-foreground">{{ v.description }}</span>
                </li>
              </ul>
            </div>

            <Collapsible v-model:open="showFile">
              <CollapsibleTrigger as-child>
                <button type="button" class="inline-flex items-center gap-1 text-sm font-medium hover:underline">
                  <ChevronRight :class="['size-4 transition-transform', showFile && 'rotate-90']" /> Dockerfile
                </button>
              </CollapsibleTrigger>
              <CollapsibleContent class="mt-2">
                <CodeBlock :code="entry.data.value.dockerfile" what="Dockerfile" />
              </CollapsibleContent>
            </Collapsible>
          </CardContent>
        </Card>

        <Alert v-if="error" variant="destructive">
          <AlertTitle>{{ error.message }}</AlertTitle>
          <AlertDescription><p v-if="error.hint">{{ error.hint }}</p></AlertDescription>
        </Alert>

        <div class="flex flex-wrap items-center gap-3">
          <Button type="submit" :disabled="deploy.isPending.value">{{ deploy.isPending.value ? 'Creating the app...' : 'Deploy' }}</Button>
          <Button as-child variant="ghost" :disabled="deploy.isPending.value"><RouterLink :to="{ name: 'new-app' }">Cancel</RouterLink></Button>
        </div>
      </form>
    </template>
  </div>
</template>
