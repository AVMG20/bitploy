<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { ChevronRight, ExternalLink } from 'lucide-vue-next'
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
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectSeparator, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { keys, useCatalogApp, useMe, useProjects } from '@/composables/queries'
import { api, ApiError } from '@/lib/api'
import { friendlyError } from '@/lib/errors'
import type { FriendlyError } from '@/lib/errors'
import { formatMB } from '@/lib/format'
import { appRoute, projectRoute } from '@/lib/appLinks'
import { aliasOf, freeAlias } from '@/lib/projects'
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

// The project it goes into: ?project=<id> from "Add" on a project, or a new one named after the app.
const NEW_PROJECT = '__new__'
const projects = useProjects()
const inProjects = computed(() => !!projects.data.value && !projects.data.value.fallback)
const projectChoice = ref(typeof route.query.project === 'string' && route.query.project ? route.query.project : NEW_PROJECT)
const intoProject = computed(() => (projectChoice.value === NEW_PROJECT ? '' : projectChoice.value))
const target = computed(() => projects.data.value?.projects.find((p) => p.id === intoProject.value) ?? null)
// Its short name in that project: the other apps reach it at this name.
const alias = ref('')
const aliasTouched = ref(false)
watch(
  [() => entry.data.value?.id, target],
  ([id, p]) => {
    if (id && !aliasTouched.value) alias.value = freeAlias(id, (p?.apps ?? []).map(aliasOf))
  },
  { immediate: true },
)
const aliasError = computed(() => {
  const a = alias.value.trim()
  if (!intoProject.value) return ''
  if (!/^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$/.test(a)) return 'Use 1 to 32 lowercase letters, numbers and dashes, like status or kuma-2.'
  if (target.value?.apps.some((x) => aliasOf(x) === a)) return `${target.value.name} already has an app called ${a}.`
  return ''
})

const bringsServices = computed(() => !!entry.data.value?.services?.length)
const deploy = useMutation({
  mutationFn: (v: { name: string; slug: string }) =>
    api.deployCatalog(props.catalogId, {
      name: v.name,
      slug: v.slug,
      domain: domain.value || undefined,
      project: inProjects.value ? intoProject.value || undefined : undefined,
      alias: inProjects.value && intoProject.value ? alias.value.trim() || undefined : undefined,
    }),
  onSuccess: async (r) => {
    qc.setQueryData(keys.app(r.app.slug), r.app)
    void qc.invalidateQueries({ queryKey: keys.apps })
    void qc.invalidateQueries({ queryKey: keys.me })
    void qc.invalidateQueries({ queryKey: keys.deployments(r.app.slug) })
    // Into a project: back to its canvas with the new app open in the panel.
    await router.push(appRoute(r.app))
  },
  onError: (e) => {
    const f = friendlyError(e)
    if (f.code === 'alias_taken' || f.code === 'alias_invalid') {
      aliasTouched.value = true
      error.value = f
    } else if (SLUG_CODES.has(f.code)) {
      editingAddress.value = true
      addressEdited.value = true
      form.setFieldError('slug', f.hint ? `${f.message} ${f.hint}` : f.message)
    } else {
      error.value = f
    }
  },
})
const submit = form.handleSubmit((v) => {
  if (aliasError.value) return
  error.value = null
  deploy.mutate(v)
})
</script>

<template>
  <div class="grid gap-6">
    <Alert v-if="notFound" variant="destructive">
      <AlertTitle>This ready-made app does not exist.</AlertTitle>
      <AlertDescription><RouterLink :to="{ name: 'ready-made' }" class="underline">Back to Ready-made</RouterLink></AlertDescription>
    </Alert>
    <QueryError v-else-if="entry.isError.value" :error="entry.error.value" @retry="entry.refetch()" />
    <div v-else-if="!entry.data.value" class="grid gap-4">
      <Skeleton class="h-14 w-72" />
      <Skeleton class="h-64 w-full" />
    </div>

    <template v-else>
      <PageHeader :title="entry.data.value.name" :description="entry.data.value.tagline">
        <template #icon><CatalogMonogram :name="entry.data.value.name" size="lg" /></template>
      </PageHeader>
      <p v-if="bringsServices" class="-mt-3 text-sm text-muted-foreground" data-testid="catalog-services">
        Comes with
        <template v-for="(sv, i) in entry.data.value.services" :key="sv.name">
          {{ i ? ', ' : '' }}<span class="font-mono text-foreground/85">{{ sv.name }}</span> ({{ sv.image }}, {{ formatMB(sv.memory_mb) }})
        </template>.
        They run next to it in {{ target ? target.name : 'its new project' }}, already connected to it.
      </p>

      <form novalidate class="grid gap-6" @submit.prevent="submit">
        <Card>
          <CardContent class="grid items-start gap-5 lg:grid-cols-2">
            <div v-if="inProjects" class="grid gap-2">
              <Label for="catalog-project">Project</Label>
              <Select v-model="projectChoice" :disabled="deploy.isPending.value">
                <SelectTrigger id="catalog-project" class="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem :value="NEW_PROJECT">New project, named after the app</SelectItem>
                  <SelectSeparator v-if="projects.data.value?.projects.length" />
                  <SelectItem v-for="p in projects.data.value?.projects ?? []" :key="p.id" :value="p.id">{{ p.name }}</SelectItem>
                </SelectContent>
              </Select>
              <p class="text-[0.8rem] text-muted-foreground">{{ target ? `It joins the apps of ${target.name} on their private network.` : 'A project of its own. You can add databases and other apps to it later.' }}</p>
            </div>
            <div v-if="inProjects && target" class="grid gap-2">
              <Label for="catalog-alias">Short name</Label>
              <Input id="catalog-alias" v-model="alias" class="font-mono" autocapitalize="off" autocomplete="off" spellcheck="false" :disabled="deploy.isPending.value" :aria-invalid="!!aliasError || undefined" @input="aliasTouched = true" />
              <p v-if="aliasError" class="text-[0.8rem] text-destructive">{{ aliasError }}</p>
              <p v-else class="text-[0.8rem] text-muted-foreground">The other apps of {{ target.name }} reach it at this name.</p>
            </div>
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
                <FormDescription v-if="!editingAddress">Where people open the app. It follows the name until you change it.</FormDescription>
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
                <CodeBlock :code="entry.data.value.dockerfile" what="Dockerfile" lang="dockerfile" />
              </CollapsibleContent>
            </Collapsible>
          </CardContent>
        </Card>

        <Alert v-if="error" variant="destructive">
          <AlertTitle>{{ error.message }}</AlertTitle>
          <AlertDescription><p v-if="error.hint">{{ error.hint }}</p></AlertDescription>
        </Alert>

        <div class="flex flex-wrap items-center gap-3">
          <Button type="submit" :disabled="deploy.isPending.value">{{ deploy.isPending.value ? 'Creating the app...' : target ? `Deploy to ${target.name}` : `Deploy ${entry.data.value?.name ?? 'app'}` }}</Button>
          <Button as-child variant="ghost" :disabled="deploy.isPending.value"><RouterLink :to="target ? projectRoute(target.id) : { name: 'ready-made' }">Cancel</RouterLink></Button>
        </div>
      </form>
    </template>
  </div>
</template>
