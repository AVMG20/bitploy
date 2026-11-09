<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
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
import { keys, useProjects, useServiceTemplates } from '@/composables/queries'
import type { ProjectList } from '@/composables/queries'
import { api } from '@/lib/api'
import { useSession } from '@/composables/useSession'
import { appRoute, projectRoute } from '@/lib/appLinks'
import { toastError } from '@/lib/notify'
import { aliasOf, ownerName, placeId, projectHealth, sortByIds, sortProjectApps } from '@/lib/projects'

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

// Your projects and the ones shared with you, in the order the server keeps
// for you (a new one on top); the one you are in shows its apps.
const currentProject = computed(() => (typeof route.params.projectId === 'string' ? route.params.projectId : ''))
const currentSlug = computed(() => (typeof route.params.slug === 'string' ? route.params.slug : ''))
const MAX = 12
const all = computed(() => projects.data.value?.projects ?? [])
// While dragging: the order the list shows, saved on drop.
const preview = ref<string[] | null>(null)
const list = computed(() => {
  const top = all.value.slice(0, MAX)
  return preview.value ? sortByIds(top, preview.value) : top
})
const more = computed(() => Math.max(0, all.value.length - MAX))
// One app per project on an old server: it keeps no order.
const canSort = computed(() => !projects.data.value?.fallback && all.value.length > 1)

const qc = useQueryClient()
/** Saves the shown projects in `shown` order, the ones past MAX after them as they were. */
async function saveOrder(shown: string[]) {
  const ids = [...shown, ...all.value.map((p) => p.id).filter((id) => !shown.includes(id))]
  if (ids.join() === all.value.map((p) => p.id).join()) return
  await qc.cancelQueries({ queryKey: keys.projects })
  qc.setQueryData<ProjectList>(keys.projects, (old) => (old ? { ...old, projects: sortByIds(old.projects, ids) } : old))
  try {
    await api.orderProjects(ids)
  } catch (e) {
    toastError(e)
  }
  void qc.invalidateQueries({ queryKey: keys.projects })
}

const dragId = ref<string | null>(null)
let dropped = false
function onDragStart(e: DragEvent, id: string) {
  // The apps under the open project are links of their own, not handles.
  if (!canSort.value || (e.target instanceof Element && e.target.closest('[data-sidebar=menu-sub]'))) {
    e.preventDefault()
    return
  }
  dragId.value = id
  dropped = false
  preview.value = list.value.map((p) => p.id)
  if (e.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', id) // Firefox starts no drag without data
  }
}
// The dragged project goes before the one under the pointer, or after it
// past its middle; deciding by the middle keeps a tall item from flipping.
function onDragOver(e: DragEvent, id: string) {
  if (!dragId.value || !preview.value) return
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
  preview.value = placeId(preview.value, dragId.value, id, e.clientY > r.top + r.height / 2)
}
function onDrop(e: DragEvent) {
  if (!dragId.value) return
  e.preventDefault()
  dropped = true
}
function onDragEnd() {
  const order = preview.value
  dragId.value = null
  preview.value = null
  if (dropped && order) void saveOrder(order)
}

// Alt+Up and Alt+Down move the focused project.
async function moveBy(id: string, step: number) {
  if (!canSort.value) return
  const ids = list.value.map((p) => p.id)
  const i = ids.indexOf(id)
  const target = ids[i + step]
  if (i < 0 || !target) return
  const done = saveOrder(placeId(ids, id, target, step > 0))
  await nextTick()
  document.querySelector<HTMLElement>(`[data-project-link="${id}"]`)?.focus()
  await done
}
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
          <SidebarMenu @dragover="dragId && $event.preventDefault()" @drop="onDrop">
            <SidebarMenuItem
              v-for="p in list"
              :key="p.id"
              :draggable="canSort"
              :class="p.id === dragId && 'opacity-50'"
              @dragstart="onDragStart($event, p.id)"
              @dragover="onDragOver($event, p.id)"
              @drop="onDrop"
              @dragend="onDragEnd"
            >
              <SidebarMenuButton as-child :is-active="p.id === currentProject && !currentSlug">
                <RouterLink
                  :to="projectRoute(p.id)"
                  :data-project-link="p.id"
                  draggable="false"
                  :aria-keyshortcuts="canSort ? 'Alt+ArrowUp Alt+ArrowDown' : undefined"
                  @click="setOpenMobile(false)"
                  @keydown.alt.up.prevent="moveBy(p.id, -1)"
                  @keydown.alt.down.prevent="moveBy(p.id, 1)"
                >
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
