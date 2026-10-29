<script setup lang="ts">
import { Monitor, Moon, Sun } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { setTheme, theme } from '@/lib/theme'
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button variant="ghost" size="icon" aria-label="Change theme">
        <Sun class="dark:hidden" />
        <Moon class="hidden dark:block" />
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end">
      <DropdownMenuItem :class="theme === 'light' && 'font-semibold'" @click="setTheme('light')">
        <Sun /> Light
      </DropdownMenuItem>
      <DropdownMenuItem :class="theme === 'dark' && 'font-semibold'" @click="setTheme('dark')">
        <Moon /> Dark
      </DropdownMenuItem>
      <DropdownMenuItem :class="theme === 'system' && 'font-semibold'" @click="setTheme('system')">
        <Monitor /> Match my device
      </DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
