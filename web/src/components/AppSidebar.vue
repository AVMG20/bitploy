<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { Box, KeyRound, Settings, ShieldCheck, Sparkles } from 'lucide-vue-next'
import BrandMark from './BrandMark.vue'
import ThemeToggle from './ThemeToggle.vue'
import UserMenu from './UserMenu.vue'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from '@/components/ui/sidebar'
import { useSession } from '@/composables/useSession'

const route = useRoute()
const { isAdmin } = useSession()
const { setOpenMobile } = useSidebar()

const items = computed(() => {
  const list = [
    { title: 'Apps', to: '/', icon: Box, active: route.path === '/' || route.path.startsWith('/apps/') },
    { title: 'Tokens', to: '/tokens', icon: KeyRound, active: route.path.startsWith('/tokens') },
    { title: 'Connect AI', to: '/connect', icon: Sparkles, active: route.path.startsWith('/connect') },
    { title: 'Settings', to: '/settings', icon: Settings, active: route.path.startsWith('/settings') },
  ]
  if (isAdmin.value) {
    list.push({ title: 'Admin', to: '/admin', icon: ShieldCheck, active: route.path.startsWith('/admin') })
  }
  return list
})
</script>

<template>
  <Sidebar collapsible="icon" variant="inset">
    <SidebarHeader>
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton size="lg" as-child tooltip="bitploy" class="hover:bg-transparent active:bg-transparent">
            <RouterLink to="/" @click="setOpenMobile(false)">
              <BrandMark class="size-8!" />
              <span class="text-[15px] font-semibold tracking-tight text-sidebar-accent-foreground">bitploy</span>
            </RouterLink>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarHeader>
    <SidebarContent>
      <SidebarGroup>
        <SidebarGroupLabel>Workspace</SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem v-for="item in items" :key="item.to">
              <SidebarMenuButton as-child :is-active="item.active" :tooltip="item.title">
                <RouterLink :to="item.to" @click="setOpenMobile(false)">
                  <component :is="item.icon" />
                  <span>{{ item.title }}</span>
                </RouterLink>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
    </SidebarContent>
    <SidebarFooter>
      <div class="flex items-center gap-1 group-data-[collapsible=icon]:flex-col">
        <UserMenu class="min-w-0 flex-1" />
        <ThemeToggle />
      </div>
    </SidebarFooter>
    <SidebarRail />
  </Sidebar>
</template>
