<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { ClipboardPaste, KeyRound, Pencil, Plus, SearchX, Trash2 } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import FormScope from '@/components/FormScope.vue'
import ListSearch from '@/components/ListSearch.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { keys, useApp } from '@/composables/queries'
import { markRestartNeeded } from '@/composables/useRestartNeeded'
import { api } from '@/lib/api'
import { parseDotenv } from '@/lib/dotenv'
import { toastError, toastOk } from '@/lib/notify'

// Environment variables: settings and secrets the app reads when it starts.
// Values are write-only; the list shows a masked hint. A change applies after a
// restart, which the app page offers as soon as one is saved.
const props = defineProps<{ slug: string }>()
const qc = useQueryClient()
const app = useApp(() => props.slug)
const env = useQuery({
  queryKey: computed(() => keys.env(props.slug)),
  queryFn: () => api.env(props.slug),
})
const list = computed(() => env.data.value ?? [])
const names = computed(() => new Set(list.value.map((v) => v.name)))

function changed() {
  markRestartNeeded(app.data.value, 'variables')
  void qc.invalidateQueries({ queryKey: keys.env(props.slug) })
}
const willRestart = computed(() => app.data.value?.status === 'running')
const appliesWhen = computed(() => (willRestart.value ? 'Restart the app to use it.' : 'The app uses it the next time it starts.'))

// ---------------------------------------------------------------- search (only worth it for a longer list)
const search = ref('')
const shown = computed(() => {
  const q = search.value.trim().toLowerCase()
  return q ? list.value.filter((v) => v.name.toLowerCase().includes(q)) : list.value
})

// ---------------------------------------------------------------- add
const NAME_RULE = /^[A-Za-z_][A-Za-z0-9_]*$/
const addForm = useForm({
  validationSchema: toTypedSchema(
    z.object({
      name: z.string().trim().min(1, 'Give the variable a name.').regex(NAME_RULE, 'Use letters, numbers and underscores only, like API_KEY.'),
      value: z.string(),
    }),
  ),
  initialValues: { name: '', value: '' },
})
const addingExisting = computed(() => names.value.has((addForm.values.name ?? '').trim()))

const save = useMutation({
  mutationFn: (v: { name: string; value: string }) => api.setEnv(props.slug, { [v.name]: v.value }, []),
  onSuccess: (_r, v) => {
    toastOk(`Saved ${v.name}`, appliesWhen.value)
    changed()
  },
  onError: (e) => toastError(e),
})
const submitAdd = addForm.handleSubmit((v) =>
  save.mutate(
    { name: v.name.trim(), value: v.value },
    {
      onSuccess: () => {
        addForm.resetForm({ values: { name: '', value: '' } })
        // Ready for the next one.
        document.querySelector<HTMLInputElement>('#env-add input')?.focus()
      },
    },
  ),
)
function onAddKey(e: KeyboardEvent) {
  // Enter saves; Shift+Enter starts a new line (for a key that spans lines).
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    void submitAdd()
  }
}

// ---------------------------------------------------------------- edit one, in its row
const editing = ref<string | null>(null)
const editValue = ref('')
async function startEdit(name: string) {
  editing.value = name
  editValue.value = ''
  await nextTick()
  document.querySelector<HTMLTextAreaElement>(`#env-edit-${name}`)?.focus()
}
function cancelEdit() {
  editing.value = null
  editValue.value = ''
}
function saveEdit() {
  const name = editing.value
  if (!name) return
  save.mutate({ name, value: editValue.value }, { onSuccess: cancelEdit })
}
function onEditKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault()
    cancelEdit()
  } else if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    saveEdit()
  }
}

// ---------------------------------------------------------------- remove
const toRemove = ref<string | null>(null)
const removeOpen = computed({
  get: () => toRemove.value !== null,
  set: (v) => {
    if (!v) toRemove.value = null
  },
})
const remove = useMutation({
  mutationFn: (name: string) => api.setEnv(props.slug, {}, [name]),
  onSuccess: (_r, name) => {
    toastOk(`Removed ${name}`)
    toRemove.value = null
    changed()
  },
  onError: (e) => toastError(e),
})

// ---------------------------------------------------------------- paste a .env file
const pasteOpen = ref(false)
const pasteText = ref('')
const parsed = computed(() => parseDotenv(pasteText.value))
const replacing = computed(() => parsed.value.vars.filter((v) => names.value.has(v.name)).length)
function openPaste() {
  pasteText.value = ''
  pasteOpen.value = true
}
const importAll = useMutation({
  mutationFn: () => api.setEnv(props.slug, Object.fromEntries(parsed.value.vars.map((v) => [v.name, v.value])), []),
  onSuccess: () => {
    const n = parsed.value.vars.length
    toastOk(`Saved ${n} ${n === 1 ? 'variable' : 'variables'}`, appliesWhen.value)
    pasteOpen.value = false
    changed()
  },
  onError: (e) => toastError(e),
})
</script>

<template>
  <div class="grid gap-4">
    <Card>
      <CardHeader class="flex flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3">
        <div class="grid min-w-0 gap-1.5">
          <CardTitle>Environment variables</CardTitle>
          <CardDescription>
            Passwords, API keys and other settings your app reads when it starts. Values are hidden once saved. A change applies after a restart.
          </CardDescription>
        </div>
        <Button variant="outline" @click="openPaste"><ClipboardPaste /> Paste .env</Button>
      </CardHeader>
      <CardContent class="grid gap-4">
        <FormScope :form="addForm">
          <form id="env-add" class="grid gap-3 rounded-lg border bg-muted/30 p-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)_auto] sm:items-start" novalidate @submit.prevent="submitAdd">
            <FormField v-slot="{ componentField }" name="name">
              <FormItem>
                <FormLabel>Name</FormLabel>
                <FormControl><Input placeholder="API_KEY" autocapitalize="off" autocomplete="off" spellcheck="false" class="font-mono" v-bind="componentField" /></FormControl>
                <FormMessage />
              </FormItem>
            </FormField>
            <FormField v-slot="{ componentField }" name="value">
              <FormItem>
                <FormLabel>Value</FormLabel>
                <FormControl>
                  <Textarea
                    rows="1"
                    placeholder="Paste the value"
                    autocomplete="off"
                    spellcheck="false"
                    class="min-h-8 resize-y py-[5px] leading-5 font-mono"
                    v-bind="componentField"
                    @keydown="onAddKey"
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            </FormField>
            <Button type="submit" class="sm:mt-[1.375rem]" :disabled="save.isPending.value && !editing">
              <Plus /> {{ addingExisting ? 'Replace' : 'Add' }}
            </Button>
            <p v-if="addingExisting" class="text-xs text-muted-foreground sm:col-span-3">
              <span class="font-mono">{{ addForm.values.name?.trim() }}</span> exists already. Saving gives it this new value.
            </p>
          </form>
        </FormScope>

        <QueryError v-if="env.isError.value" :error="env.error.value" @retry="env.refetch()" />
        <Skeleton v-else-if="env.isPending.value" class="h-24 w-full" />
        <EmptyState v-else-if="!list.length" :icon="KeyRound" title="No variables yet." description="Add one above, like DATABASE_URL, or paste the contents of a .env file." />
        <template v-else>
          <ListSearch v-if="list.length > 6" v-model="search" class="sm:max-w-xs" placeholder="Find a variable" label="Find a variable" />
          <p v-if="!shown.length" class="flex items-center gap-2 py-6 text-sm text-muted-foreground"><SearchX class="size-4" /> No variable is called “{{ search.trim() }}”.</p>
          <ul v-else class="divide-y rounded-lg border">
            <li v-for="v in shown" :key="v.name" class="px-3 py-2">
              <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
                <span class="font-mono text-sm font-medium break-all">{{ v.name }}</span>
                <span class="font-mono text-sm text-muted-foreground">{{ v.masked }}</span>
                <span class="text-xs text-muted-foreground">changed <RelativeTime :date="v.updated_at" /></span>
                <div v-if="editing !== v.name" class="ml-auto flex gap-1">
                  <Button size="sm" variant="ghost" :aria-label="`Change the value of ${v.name}`" @click="startEdit(v.name)"><Pencil /> Change</Button>
                  <Button size="sm" variant="ghost" class="text-muted-foreground hover:text-destructive" :aria-label="`Remove ${v.name}`" @click="toRemove = v.name"><Trash2 /> Remove</Button>
                </div>
              </div>
              <form v-if="editing === v.name" class="mt-2 grid gap-2" novalidate @submit.prevent="saveEdit">
                <Label :for="`env-edit-${v.name}`" class="sr-only">New value for {{ v.name }}</Label>
                <Textarea
                  :id="`env-edit-${v.name}`"
                  v-model="editValue"
                  rows="1"
                  placeholder="The new value"
                  autocomplete="off"
                  spellcheck="false"
                  class="min-h-8 resize-y py-[5px] leading-5 font-mono"
                  @keydown="onEditKey"
                />
                <div class="flex flex-wrap items-center gap-2">
                  <Button type="submit" size="sm" :disabled="save.isPending.value">{{ save.isPending.value ? 'Saving...' : 'Save new value' }}</Button>
                  <Button type="button" size="sm" variant="ghost" :disabled="save.isPending.value" @click="cancelEdit">Cancel</Button>
                  <span class="text-xs text-muted-foreground">The old value is not shown. Enter saves, Esc cancels.</span>
                </div>
              </form>
            </li>
          </ul>
        </template>
      </CardContent>
    </Card>

    <Dialog v-model:open="pasteOpen">
      <DialogContent class="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Paste a .env file</DialogTitle>
          <DialogDescription>One NAME=value per line. Comments and empty lines are left out.</DialogDescription>
        </DialogHeader>
        <Label for="env-paste" class="sr-only">Contents of the .env file</Label>
        <Textarea id="env-paste" v-model="pasteText" rows="8" placeholder="DATABASE_URL=...&#10;API_KEY=..." spellcheck="false" autocomplete="off" class="max-h-72 font-mono text-xs" />
        <div v-if="pasteText.trim()" class="grid gap-2 text-sm">
          <p v-if="parsed.vars.length">
            {{ parsed.vars.length }} {{ parsed.vars.length === 1 ? 'variable' : 'variables' }}<template v-if="replacing">, {{ replacing }} of them replace{{ replacing === 1 ? 's' : '' }} a value you have</template>.
          </p>
          <ul v-if="parsed.vars.length" class="flex max-h-28 flex-wrap gap-1.5 overflow-y-auto">
            <li v-for="v in parsed.vars" :key="v.name">
              <Badge :variant="names.has(v.name) ? 'outline' : 'secondary'" class="font-mono" :title="names.has(v.name) ? 'Replaces the value you have' : 'New'">{{ v.name }}</Badge>
            </li>
          </ul>
          <p v-if="parsed.skipped.length" class="text-xs text-destructive">
            Not understood, left out: line {{ parsed.skipped.map((s) => s.line).join(', ') }}.
          </p>
        </div>
        <DialogFooter>
          <Button variant="ghost" :disabled="importAll.isPending.value" @click="pasteOpen = false">Cancel</Button>
          <Button :disabled="!parsed.vars.length || importAll.isPending.value" @click="importAll.mutate()">
            {{ importAll.isPending.value ? 'Saving...' : parsed.vars.length ? `Save ${parsed.vars.length} ${parsed.vars.length === 1 ? 'variable' : 'variables'}` : 'Save' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      v-model:open="removeOpen"
      :title="`Remove ${toRemove ?? 'this variable'}?`"
      :description="`${toRemove ?? 'The variable'} will be gone the next time the app starts. Your app may stop working if it needs it.`"
      confirm-label="Remove"
      :loading="remove.isPending.value"
      @confirm="toRemove && remove.mutate(toRemove)"
    />
  </div>
</template>
