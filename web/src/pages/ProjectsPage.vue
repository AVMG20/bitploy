<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { Container, Plus, Sparkles } from 'lucide-vue-next'
import ActivityRail from '@/components/home/ActivityRail.vue'
import AttentionList from '@/components/home/AttentionList.vue'
import FirstRun from '@/components/home/FirstRun.vue'
import ServerCard from '@/components/home/ServerCard.vue'
import { useServerUsage } from '@/components/home/useServerUsage'
import QueryError from '@/components/QueryError.vue'
import StorageAppsDialog from '@/components/StorageAppsDialog.vue'
import ProjectCard from '@/components/project/ProjectCard.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useAiConnected, useProjects, useServiceTemplates } from '@/composables/queries'
import { useSession } from '@/composables/useSession'

// Home: the server's limits on top, what needs you, then every project as a
// card in the order of the sidebar (dragged there; a new project on top), with
// the latest activity beside them.
const projects = useProjects()
const templates = useServiceTemplates()
const { user } = useSession()
const { me, attention, storageUsed } = useServerUsage()
const aiConnected = useAiConnected()

const all = computed(() => projects.data.value?.projects ?? [])
const apps = computed(() => all.value.flatMap((p) => p.apps))
// "See storage per app" shows what each of your apps takes.
const storageOpen = ref(false)

const firstName = computed(() => (user.value?.name || user.value?.email.split('@')[0] || '').trim().split(/\s+/)[0] ?? '')
const greeting = computed(() => {
  const h = new Date().getHours()
  const part = h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening'
  return firstName.value ? `${part}, ${firstName.value}` : part
})
const today = new Date().toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' })
</script>

<template>
  <div class="grid gap-6">
    <FirstRun v-if="!projects.isPending.value && !projects.isError.value && !all.length" :name="firstName" />

    <template v-else>
      <header class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="text-sm text-muted-foreground">{{ today }}</p>
          <h1 class="mt-0.5 text-2xl font-semibold tracking-tight">{{ greeting }}</h1>
        </div>
        <div class="flex gap-2">
          <Button v-if="aiConnected === false" variant="outline" as-child><RouterLink :to="{ name: 'connect' }"><Sparkles /> Connect AI</RouterLink></Button>
          <Button as-child><RouterLink :to="{ name: 'new-app' }"><Plus /> New project</RouterLink></Button>
        </div>
      </header>

      <ServerCard @storage="storageOpen = true" />

      <AttentionList :apps="apps" :resources="attention" @storage="storageOpen = true" />

      <StorageAppsDialog v-if="me.data.value" v-model:open="storageOpen" :projects="all" :owner-id="me.data.value.user.id" :used-mb="storageUsed" />

      <QueryError v-if="projects.isError.value && !projects.data.value" :error="projects.error.value" @retry="projects.refetch()" />

      <div v-else class="grid gap-3">
        <!-- The heading sits above both columns, so the first project cards and the activity card start level. -->
        <h2 class="text-sm font-semibold">Projects</h2>
        <div class="grid gap-8 xl:grid-cols-[minmax(0,1fr)_19rem]">
          <section class="grid content-start gap-3" aria-label="Projects">
            <div v-if="projects.isPending.value" class="grid gap-4 md:grid-cols-2">
              <Skeleton v-for="i in 3" :key="i" class="h-56 rounded-xl" />
            </div>
            <template v-else>
              <div class="grid gap-4 md:grid-cols-2">
                <ProjectCard v-for="p in all" :key="p.id" :project="p" :templates="templates.data.value ?? []" />
                <RouterLink
                  :to="{ name: 'new-app' }"
                  class="grid min-h-48 place-items-center rounded-xl border border-dashed p-4 text-sm text-muted-foreground transition-colors hover:border-foreground/30 hover:text-foreground"
                >
                  <span class="grid justify-items-center gap-2 text-center">
                    <span class="flex size-9 items-center justify-center rounded-lg border bg-card"><Plus class="size-4" /></span>
                    New project
                    <span class="text-xs">From GitHub, a folder, an image or your AI</span>
                  </span>
                </RouterLink>
              </div>
              <p v-if="projects.data.value?.fallback" class="text-xs text-muted-foreground">
                <Container class="mr-1 inline size-3.5 align-[-2px]" />This server shows each app as its own project. It cannot put several apps in one project.
              </p>
            </template>
          </section>

          <aside class="grid content-start gap-4" aria-label="Activity">
            <ActivityRail />
          </aside>
        </div>
      </div>
    </template>
  </div>
</template>
