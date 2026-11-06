<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Paperclip } from 'lucide-vue-next'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { affinityOf, bytesToBase64, effectiveAffinity, isRowidAlias, parseInput, sizeLabel } from '@/lib/dbvalue'
import type { Affinity, InputKind } from '@/lib/dbvalue'
import { friendlyError } from '@/lib/errors'
import type { DbColumn, DbTable, DbValue } from '@/lib/types'

// The form for a new row. (Existing rows are edited in the grid, cell by cell.)
// A field is in one of three modes:
//   value    what is typed (or the chosen file)
//   null     NULL
//   default  left out, so the column's default (or the next rowid) applies
type Mode = 'value' | 'null' | 'default'

interface Field {
  col: DbColumn
  aff: Affinity
  mode: Mode
  text: string
  /** Text or number, for a column of no fixed type (its value could be either). */
  kind: InputKind
  /** A file picked for a BLOB: base64 and its name. */
  blob: string | null
  fileName: string
  hint: string
  error: string
  /** NOT NULL with no default (and not the rowid): it needs a value, an empty one only if asked for. */
  required: boolean
  allowEmpty: boolean
}

const props = defineProps<{ table: DbTable; saving: boolean; error: unknown }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ submit: [values: Record<string, DbValue>] }>()

const MAX_FILE = 4 * 1024 * 1024
const fields = ref<Field[]>([])

function hintOf(col: DbColumn): string {
  if (isRowidAlias(props.table, col)) return 'Automatic (next number)'
  if (col.default !== null) return `Default: ${col.default}`
  return col.notnull ? 'Required' : 'NULL'
}

function build(): Field[] {
  return props.table.columns
    .filter((c) => !c.hidden && !c.generated)
    .map((col) => {
      const optional = isRowidAlias(props.table, col) || col.default !== null || !col.notnull
      return { col, aff: affinityOf(col.type), mode: optional ? 'default' : 'value', text: '', kind: 'text', blob: null, fileName: '', hint: hintOf(col), error: '', required: !optional, allowEmpty: false }
    })
}

watch(
  open,
  (o) => {
    if (o) fields.value = build()
  },
  { immediate: true },
)

const apiError = computed(() => (props.error ? friendlyError(props.error) : null))

function clearFile(f: Field) {
  f.blob = null
  f.fileName = ''
}
function setMode(f: Field, mode: Mode) {
  f.mode = mode
  f.error = ''
  if (mode !== 'value') clearFile(f)
}
function onType(f: Field) {
  if (f.mode !== 'value') f.mode = 'value'
  f.error = ''
  f.allowEmpty = false
}

async function pickFile(f: Field, e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (file.size > MAX_FILE) {
    f.error = `Pick a file up to ${sizeLabel(MAX_FILE)}.`
    return
  }
  f.blob = bytesToBase64(new Uint8Array(await file.arrayBuffer()))
  f.fileName = file.name
  f.mode = 'value'
  f.error = ''
}

function submit() {
  if (props.saving) return
  const out: Record<string, DbValue> = {}
  let ok = true
  for (const f of fields.value) {
    f.error = ''
    if (f.mode === 'default') continue
    if (f.mode === 'null') {
      out[f.col.name] = null
    } else if (f.blob) {
      out[f.col.name] = { blob: f.blob }
    } else if (f.required && f.text === '' && !f.allowEmpty) {
      f.error = 'Required. Type a value.'
      ok = false
    } else {
      const p = parseInput(effectiveAffinity(f.aff, f.kind), f.text)
      if (p.ok) out[f.col.name] = p.value
      else {
        f.error = p.error
        ok = false
      }
    }
  }
  if (ok) emit('submit', out)
}

const modes = (f: Field): { mode: Mode; label: string; disabled?: boolean }[] => [
  { mode: 'value', label: 'Value' },
  { mode: 'null', label: 'NULL', disabled: f.col.notnull },
  { mode: 'default', label: 'Default' },
]

/** Only a column of no fixed type (or a BLOB one) can hold either kind, so only there the person says which. */
const takesKind = (f: Field) => f.aff === 'blob' && f.mode === 'value' && !f.blob
const takesFile = (f: Field) => f.aff === 'blob' && f.col.type.toUpperCase().includes('BLOB')
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="w-full sm:max-w-lg" :show-close-button="!saving">
      <form class="flex min-h-0 flex-1 flex-col" @submit.prevent="submit" @keydown.ctrl.enter.prevent="submit" @keydown.meta.enter.prevent="submit">
        <SheetHeader class="border-b">
          <SheetTitle class="pr-8 font-mono text-sm">New row in {{ table.name }}</SheetTitle>
          <SheetDescription>Leave a column on Default to let the database fill it in.</SheetDescription>
        </SheetHeader>

        <div class="grid flex-1 content-start gap-4 overflow-y-auto px-4 py-1">
          <Alert v-if="apiError" variant="destructive">
            <AlertTitle>{{ apiError.message }}</AlertTitle>
            <AlertDescription v-if="apiError.hint">{{ apiError.hint }}</AlertDescription>
          </Alert>

          <div v-for="f in fields" :key="f.col.name" class="grid gap-1.5">
            <div class="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
              <label :for="`f-${f.col.name}`" class="flex min-w-0 items-center gap-1.5 text-[13px] font-medium">
                <span class="truncate font-mono">{{ f.col.name }}</span>
                <span class="shrink-0 text-xs font-normal text-muted-foreground">{{ f.col.type || 'any' }}</span>
                <Badge v-if="f.col.pk > 0" variant="secondary">key</Badge>
                <Badge v-if="f.required" variant="outline">required</Badge>
                <Badge v-else-if="f.col.notnull" variant="outline">not null</Badge>
              </label>
              <div class="inline-flex rounded-md border p-px" role="group" :aria-label="`Value of ${f.col.name}`">
                <button
                  v-for="o in modes(f)"
                  :key="o.mode"
                  type="button"
                  class="rounded px-2 py-0.5 text-xs font-medium transition-colors focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none disabled:opacity-40"
                  :class="f.mode === o.mode ? 'bg-muted text-foreground' : 'text-muted-foreground hover:text-foreground'"
                  :disabled="o.disabled"
                  :aria-pressed="f.mode === o.mode"
                  @click="setMode(f, o.mode)"
                >
                  {{ o.label }}
                </button>
              </div>
            </div>

            <p v-if="f.blob" class="rounded-lg border bg-muted/50 px-2.5 py-2 text-xs text-muted-foreground">New BLOB from {{ f.fileName }} ({{ sizeLabel(Math.floor((f.blob.length * 3) / 4)) }}).</p>
            <Textarea
              v-else-if="f.aff === 'text' || f.aff === 'blob'"
              :id="`f-${f.col.name}`"
              v-model="f.text"
              class="max-h-48 min-h-8 py-1.5 font-mono text-[13px]"
              :placeholder="f.mode === 'null' ? 'NULL' : f.mode === 'default' ? f.hint : ''"
              :disabled="f.mode === 'null'"
              :aria-invalid="!!f.error"
              autocomplete="off"
              spellcheck="false"
              @update:model-value="onType(f)"
            />
            <Input
              v-else
              :id="`f-${f.col.name}`"
              v-model="f.text"
              class="font-mono text-[13px]"
              :placeholder="f.mode === 'null' ? 'NULL' : f.mode === 'default' ? f.hint : ''"
              :disabled="f.mode === 'null'"
              :aria-invalid="!!f.error"
              inputmode="decimal"
              autocomplete="off"
              spellcheck="false"
              @update:model-value="onType(f)"
            />

            <div v-if="takesKind(f) || takesFile(f)" class="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs text-muted-foreground">
              <div v-if="takesKind(f)" class="flex items-center gap-2" role="group" :aria-label="`Kind of value for ${f.col.name}`">
                <span>Store as</span>
                <div class="inline-flex rounded-md border p-px">
                  <button
                    v-for="k in ['text', 'number'] as const"
                    :key="k"
                    type="button"
                    class="rounded px-2 py-0.5 text-xs font-medium capitalize transition-colors focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
                    :class="f.kind === k ? 'bg-muted text-foreground' : 'text-muted-foreground hover:text-foreground'"
                    :aria-pressed="f.kind === k"
                    @click="((f.kind = k), (f.error = ''))"
                  >
                    {{ k }}
                  </button>
                </div>
              </div>
              <template v-if="takesFile(f)">
                <label class="inline-flex cursor-pointer items-center gap-1 rounded-md border px-2 py-1 font-medium text-foreground hover:bg-muted">
                  <Paperclip class="size-3" /> Pick a file
                  <input type="file" class="sr-only" @change="pickFile(f, $event)" />
                </label>
                <button v-if="f.blob" type="button" class="underline hover:text-foreground" @click="clearFile(f), (f.mode = 'default')">Remove</button>
              </template>
            </div>

            <p v-if="f.error" class="text-[0.8rem] font-medium text-destructive">
              {{ f.error }}
              <button v-if="f.required && f.error.startsWith('Required') && f.aff !== 'integer' && f.aff !== 'real'" type="button" class="ml-1 font-normal underline" @click="((f.allowEmpty = true), (f.error = ''))">Use empty text instead</button>
            </p>
            <p v-else-if="f.mode === 'default'" class="text-xs text-muted-foreground">{{ f.hint }}</p>
          </div>
        </div>

        <SheetFooter class="flex-row justify-end border-t">
          <Button type="button" variant="outline" :disabled="saving" @click="open = false">Cancel</Button>
          <Button type="submit" :disabled="saving">{{ saving ? 'Saving...' : 'Insert row' }}</Button>
        </SheetFooter>
      </form>
    </SheetContent>
  </Sheet>
</template>
