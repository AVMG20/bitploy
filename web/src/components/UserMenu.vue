<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { ChevronsUpDown, LogOut, Monitor, Moon, Settings, Sun } from 'lucide-vue-next'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useSession } from '@/composables/useSession'
import { api } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { toastError } from '@/lib/notify'
import { setTheme, theme, type ThemeChoice } from '@/lib/theme'

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
      <Button
        variant="ghost"
        class="h-11 w-full justify-start gap-2.5 px-2 text-left hover:bg-sidebar-accent aria-expanded:bg-sidebar-accent group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:p-0 group-data-[collapsible=icon]:justify-center"
        aria-label="Account menu"
      >
        <Avatar class="size-7">
          <AvatarFallback class="bg-primary text-[11px] font-semibold text-primary-foreground">{{ initials }}</AvatarFallback>
        </Avatar>
        <span class="grid min-w-0 flex-1 leading-tight group-data-[collapsible=icon]:hidden">
          <span class="truncate text-sm font-medium text-sidebar-accent-foreground">{{ user?.name || user?.email }}</span>
          <span v-if="user?.name" class="truncate text-xs font-normal text-muted-foreground">{{ user?.email }}</span>
        </span>
        <ChevronsUpDown class="size-3.5 opacity-50 group-data-[collapsible=icon]:hidden" />
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent side="top" align="start" class="w-(--reka-dropdown-menu-trigger-width) min-w-56">
      <DropdownMenuItem @click="router.push('/settings')"><Settings /> Settings</DropdownMenuItem>
      <DropdownMenuSeparator />
      <DropdownMenuLabel class="text-xs font-normal text-muted-foreground">Theme</DropdownMenuLabel>
      <DropdownMenuRadioGroup :model-value="theme" @update:model-value="(v) => setTheme(v as ThemeChoice)">
        <DropdownMenuRadioItem value="light"><Sun /> Light</DropdownMenuRadioItem>
        <DropdownMenuRadioItem value="dark"><Moon /> Dark</DropdownMenuRadioItem>
        <DropdownMenuRadioItem value="system"><Monitor /> Match my device</DropdownMenuRadioItem>
      </DropdownMenuRadioGroup>
      <DropdownMenuSeparator />
      <DropdownMenuItem @click="logout"><LogOut /> Log out</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
