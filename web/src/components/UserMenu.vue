<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { ChevronDown, LogOut, Settings } from 'lucide-vue-next'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useSession } from '@/composables/useSession'
import { api } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { toastError } from '@/lib/notify'

const router = useRouter()
const { user } = useSession()

const initials = computed(() => {
  const n = user.value?.name || user.value?.email || '?'
  return n
    .split(/\s+/)
    .map((p) => p[0])
    .slice(0, 2)
    .join('')
    .toUpperCase()
})

async function logout() {
  try {
    await api.logout()
  } catch (e) {
    toastError(e)
  }
  queryClient.clear()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button variant="ghost" class="gap-2 px-1.5" aria-label="Account menu">
        <Avatar class="size-6">
          <AvatarFallback class="text-[10px]">{{ initials }}</AvatarFallback>
        </Avatar>
        <span class="hidden max-w-40 truncate sm:inline">{{ user?.name || user?.email }}</span>
        <ChevronDown class="size-3.5 opacity-60" />
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" class="w-56">
      <DropdownMenuLabel class="font-normal">
        <p class="truncate font-medium">{{ user?.name }}</p>
        <p class="truncate text-xs text-muted-foreground">{{ user?.email }}</p>
      </DropdownMenuLabel>
      <DropdownMenuSeparator />
      <DropdownMenuItem @click="router.push('/settings')"><Settings /> Settings</DropdownMenuItem>
      <DropdownMenuItem @click="logout"><LogOut /> Log out</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
