import { computed } from 'vue'
import type { MaybeRefOrGetter } from 'vue'
import { appRole, projectLinks, sortProjectApps } from '@/lib/projects'
import type { App } from '@/lib/types'
import { useProject, useProjectEnv, useServiceTemplates } from './queries'

/**
 * A project with what its pages need: the apps in canvas order, their
 * variables, who talks to whom, and what each app is for. The project page
 * and its tabs share the cached requests.
 */
export function useProjectData(id: MaybeRefOrGetter<string>) {
  const { list, project } = useProject(id)
  const templates = useServiceTemplates()
  const tpls = computed(() => templates.data.value ?? [])
  // A server without projects: each app is its own project, and nothing can be added to it.
  const fallback = computed(() => !!list.data.value?.fallback)
  const apps = computed(() => sortProjectApps(project.value?.apps ?? [], tpls.value))
  const env = useProjectEnv(apps)
  const links = computed(() => projectLinks(apps.value, env.value))
  const roles = computed(() => new Map(apps.value.map((a) => [a.id, appRole(a, tpls.value)])))
  const roleOf = (a: App) => roles.value.get(a.id) ?? appRole(a, tpls.value)
  return { list, project, templates, tpls, fallback, apps, env, links, roleOf }
}
