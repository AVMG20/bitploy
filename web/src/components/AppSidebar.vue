<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { Box, Boxes, Plus, Settings, ShieldCheck, Sparkles, Users } from 'lucide-vue-next'
import BrandMark from './BrandMark.vue'
import StatusDot from './StatusDot.vue'
import UserMenu from './UserMenu.vue'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupAction,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarRail,
  useSidebar,
} from '@/components/ui/sidebar'
import { useProjects, useServiceTemplates } from '@/composables/queries'
import { useSession } from '@/composables/useSession'
import { appRoute, projectRoute } from '@/lib/appLinks'
import { aliasOf, ownerName, projectHealth, sortProjectApps } from '@/lib/projects'

const route = useRoute()
const { isAdmin } = useSession()
const { setOpenMobile } = useSidebar()
const projects = useProjects()
const templates = useServiceTemplates()

const items = computed(() => {
  const list = [
    { title: 'Projects', to: '/', icon: Box, active: route.path === '/' || route.path === '/new' },
    { title: 'Ready-made', to: '/ready-made', icon: Boxes, active: route.path.startsWith('/ready-made') || route.name === 'new-catalog' },
    { title: 'Connect AI', to: '/connect', icon: Sparkles, active: route.path.startsWith('/connect') },
    { title: 'Settings', to: '/settings', icon: Settings, active: route.path.startsWith('/settings') },
  ]
  if (isAdmin.value) {
    list.push({ title: 'Admin', to: '/admin', icon: ShieldCheck, active: route.path.startsWith('/admin') })
  }
  return list
})

// Your projects and the ones shared with you, newest first; the one you are in shows its apps.
const currentProject = computed(() => (typeof route.params.projectId === 'string' ? route.params.projectId : ''))
const currentSlug = computed(() => (typeof route.params.slug === 'string' ? route.params.slug : ''))
const MAX = 12
const list = computed(() =>
  [...(projects.data.value?.projects ?? [])].sort((a, b) => b.created_at.localeCompare(a.created_at)).slice(0, MAX),
)
const more = computed(() => Math.max(0, (projects.data.value?.projects.length ?? 0) - MAX))
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

      <SidebarGroup v-if="list.length" class="group-data-[collapsible=icon]:hidden">
        <SidebarGroupLabel>Your projects</SidebarGroupLabel>
        <SidebarGroupAction as-child title="New project">
          <RouterLink :to="{ name: 'new-app' }" @click="setOpenMobile(false)"><Plus /><span class="sr-only">New project</span></RouterLink>
        </SidebarGroupAction>
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem v-for="p in list" :key="p.id">
              <SidebarMenuButton as-child :is-active="p.id === currentProject && !currentSlug">
                <RouterLink :to="projectRoute(p.id)" @click="setOpenMobile(false)">
                  <StatusDot :status="projectHealth(p.apps).status === 'empty' ? 'new' : projectHealth(p.apps).status" class="mx-1" />
                  <span class="truncate">{{ p.name }}</span>
                  <!-- Not a span: the button truncates its last span, which cut the count off next to a long name. -->
                  <small v-if="p.shared || p.apps.length > 1" class="ml-auto flex shrink-0 items-center gap-1.5 pl-1 text-xs text-muted-foreground tabular-nums">
                    <span v-if="p.shared" class="inline-flex" :title="`Shared by ${ownerName(p)}`"><Users class="size-3.5!" aria-hidden="true" /><span class="sr-only">Shared by {{ ownerName(p) }}</span></span>
                    <template v-if="p.apps.length > 1">{{ p.apps.length }}</template>
                  </small>
                </RouterLink>
              </SidebarMenuButton>
              <SidebarMenuSub v-if="p.id === currentProject && p.apps.length > 1">
                <SidebarMenuSubItem v-for="a in sortProjectApps(p.apps, templates.data.value ?? [])" :key="a.id">
                  <SidebarMenuSubButton as-child :is-active="a.slug === currentSlug">
                    <RouterLink :to="appRoute(a)" @click="setOpenMobile(false)">
                      <span class="truncate font-mono text-xs">{{ aliasOf(a) }}</span>
                    </RouterLink>
                  </SidebarMenuSubButton>
                </SidebarMenuSubItem>
              </SidebarMenuSub>
            </SidebarMenuItem>
            <SidebarMenuItem v-if="more">
              <SidebarMenuButton as-child class="text-muted-foreground">
                <RouterLink to="/" @click="setOpenMobile(false)"><span>And {{ more }} more</span></RouterLink>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
    </SidebarContent>
    <SidebarFooter>
      <UserMenu />
    </SidebarFooter>
    <SidebarRail />
  </Sidebar>
</template>
