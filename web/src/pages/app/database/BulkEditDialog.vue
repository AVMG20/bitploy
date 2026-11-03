<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { affinityOf, effectiveAffinity, parseInput } from '@/lib/dbvalue'
import type { InputKind } from '@/lib/dbvalue'
import { friendlyError } from '@/lib/errors'
import type { DbTable, DbValue } from '@/lib/types'

// Set one column to one value on all selected rows.
const props = defineProps<{ table: DbTable; count: number; saving: boolean; error: unknown }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ submit: [column: string, value: DbValue] }>()

const columns = computed(() => props.table.columns.filter((c) => !c.hidden && !c.generated))
const column = ref('')
const isNull = ref(false)
const text = ref('')
const kind = ref<InputKind>('text')
const problem = ref('')

const col = computed(() => columns.value.find((c) => c.name === column.value))
const aff = computed(() => affinityOf(col.value?.type ?? ''))
const apiError = computed(() => (props.error ? friendlyError(props.error) : null))
const rows = computed(() => `${props.count} ${props.count === 1 ? 'row' : 'rows'}`)

watch(open, (o) => {
  if (!o) return
  column.value = columns.value.find((c) => c.pk === 0)?.name ?? columns.value[0]?.name ?? ''
  isNull.value = false
  text.value = ''
  kind.value = 'text'
  problem.value = ''
})
watch(column, () => {
  isNull.value = false
  problem.value = ''
})

function submit() {
  if (props.saving || !col.value) return
  if (isNull.value) {
    emit('submit', col.value.name, null)
    return
  }
  const p = parseInput(effectiveAffinity(aff.value, kind.value), text.value)
  if (!p.ok) problem.value = p.error
  else emit('submit', col.value.name, p.value)
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-md">
      <form class="grid gap-4" @submit.prevent="submit">
        <DialogHeader>
          <DialogTitle>Edit {{ rows }}</DialogTitle>
          <DialogDescription>Choose a column and the value to give it on every selected row.</DialogDescription>
        </DialogHeader>

        <Alert v-if="apiError" variant="destructive">
          <AlertTitle>{{ apiError.message }}</AlertTitle>
          <AlertDescription v-if="apiError.hint">{{ apiError.hint }}</AlertDescription>
        </Alert>

        <div class="grid gap-3">
          <Select v-model="column">
            <SelectTrigger class="w-full" aria-label="Column"><SelectValue placeholder="Column" /></SelectTrigger>
            <SelectContent>
              <SelectItem v-for="c in columns" :key="c.name" :value="c.name" class="font-mono text-[13px]">{{ c.name }}</SelectItem>
            </SelectContent>
          </Select>
          <div class="flex items-center gap-2">
            <Input v-model="text" class="font-mono text-[13px]" :placeholder="isNull ? 'NULL' : 'Value'" :disabled="isNull" aria-label="Value" autocomplete="off" spellcheck="false" @update:model-value="problem = ''" />
            <Button v-if="col && !col.notnull" type="button" :variant="isNull ? 'secondary' : 'outline'" size="sm" :aria-pressed="isNull" @click="isNull = !isNull">NULL</Button>
          </div>
          <div v-if="aff === 'blob' && !isNull" class="flex items-center gap-2 text-xs text-muted-foreground" role="group" aria-label="Kind of value">
            <span>Store as</span>
            <div class="inline-flex rounded-md border p-px">
              <button
                v-for="k in ['text', 'number'] as const"
                :key="k"
                type="button"
                class="rounded px-2 py-0.5 text-xs font-medium capitalize transition-colors"
                :class="kind === k ? 'bg-muted text-foreground' : 'text-muted-foreground hover:text-foreground'"
                :aria-pressed="kind === k"
                @click="((kind = k), (problem = ''))"
              >
                {{ k }}
              </button>
            </div>
          </div>
          <p v-if="problem" class="text-[0.8rem] font-medium text-destructive">{{ problem }}</p>
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" :disabled="saving" @click="open = false">Cancel</Button>
          <Button type="submit" :disabled="saving || !col">{{ saving ? 'Saving...' : `Set ${column} on ${rows}` }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
