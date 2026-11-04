<script setup lang="ts">
import { computed } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { ChevronDown } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { toastError } from '@/lib/notify'
import type { AdminUser } from '@/lib/types'

/** Which extra app domains one person may use. Shown only when there are extra domains. */
const props = defineProps<{ user: AdminUser }>()
const qc = useQueryClient()
const domains = useQuery({ queryKey: keys.adminAppDomains, queryFn: api.adminAppDomains, staleTime: 30_000 })
const extra = computed(() => (domains.data.value?.items ?? []).filter((d) => !d.default))
const granted = computed(() => new Set(props.user.app_domain_ids ?? []))

const save = useMutation({
  mutationFn: (ids: string[]) => api.setUserAppDomains(props.user.id, ids),
  onSuccess: (u) => {
    qc.setQueryData<AdminUser[]>(keys.adminUsers, (list) => list?.map((x) => (x.id === u.id ? u : x)))
    void qc.invalidateQueries({ queryKey: keys.adminUsers })
    void qc.invalidateQueries({ queryKey: keys.adminAppDomains })
  },
  onError: (e) => toastError(e),
})

function toggle(id: string, on: boolean) {
  const next = new Set(granted.value)
  if (on) next.add(id)
  else next.delete(id)
  save.mutate([...next])
}

const label = computed(() => {
  const n = extra.value.filter((d) => granted.value.has(d.id)).length
  return n === 0 ? 'Default domain' : n === 1 ? '1 more domain' : `${n} more domains`
})
</script>

<template>
  <template v-if="extra.length">
    <span v-if="user.role === 'admin'" class="px-2 text-xs whitespace-nowrap text-muted-foreground">All domains</span>
    <DropdownMenu v-else>
      <DropdownMenuTrigger as-child>
        <Button size="sm" variant="outline" :aria-label="`Domains for ${user.name}`" :disabled="save.isPending.value">
          {{ label }} <ChevronDown class="size-3.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" class="w-72">
        <DropdownMenuLabel class="text-xs font-normal text-muted-foreground">Where {{ user.name }} can put apps</DropdownMenuLabel>
        <DropdownMenuCheckboxItem :model-value="true" disabled>
          <span class="truncate font-mono text-[13px]">{{ domains.data.value?.items.find((d) => d.default)?.domain }}</span>
          <span class="ml-auto text-xs text-muted-foreground">always</span>
        </DropdownMenuCheckboxItem>
        <DropdownMenuCheckboxItem
          v-for="d in extra"
          :key="d.id"
          :model-value="granted.has(d.id)"
          :disabled="save.isPending.value"
          @update:model-value="(v: boolean | 'indeterminate') => toggle(d.id, v === true)"
          @select="(e: Event) => e.preventDefault()"
        >
          <span class="truncate font-mono text-[13px]">{{ d.domain }}</span>
        </DropdownMenuCheckboxItem>
        <DropdownMenuSeparator />
        <p class="px-2 py-1.5 text-xs text-muted-foreground">Taking a domain away does not move apps that already use it.</p>
      </DropdownMenuContent>
    </DropdownMenu>
  </template>
</template>
