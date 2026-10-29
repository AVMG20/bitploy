<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { Box, KeyRound, Settings, ShieldCheck, Sparkles } from 'lucide-vue-next'
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
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
  <Sidebar collapsible="icon">
    <SidebarHeader>
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton size="lg" as-child tooltip="bitploy">
            <RouterLink to="/" @click="setOpenMobile(false)">
              <div class="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
                <Box class="size-4" />
              </div>
              <span class="text-base font-semibold">bitploy</span>
            </RouterLink>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarHeader>
    <SidebarContent>
      <SidebarGroup>
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
    <SidebarRail />
  </Sidebar>
</template>
