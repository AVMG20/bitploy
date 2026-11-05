import { computed, toValue } from 'vue'
import type { MaybeRefOrGetter } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { api } from '@/lib/api'
import { isPublicApp } from '@/lib/projects'
import { isMulti } from '@/lib/services'
import type { App } from '@/lib/types'
import { keys } from './queries'
import { useAppActions } from './useAppActions'
import { clearRestartNeeded, useRestartNeeded } from './useRestartNeeded'

/**
 * What an app's header offers, shared by the app page and the side panel on
 * the project canvas, so both show the same buttons for the same app:
 * the main button (deploy the newest commit, upload a new version, update a
 * ready-made app, or open it), restart, start and stop, and the changes that
 * wait for a restart.
 */
export function useAppControls(slug: MaybeRefOrGetter<string>, app: MaybeRefOrGetter<App | null | undefined>) {
  const actions = useAppActions(slug)
  const data = computed(() => toValue(app) ?? null)
  const isWeb = computed(() => !!data.value && isPublicApp(data.value))
  const multi = computed(() => isMulti(data.value))
  const dep = computed(() => data.value?.current_deployment ?? null)
  // A failed first deploy has no live version, but its source (or the linked repository) can be built again.
  const canRebuild = computed(() => !!data.value && (!!dep.value || !!data.value.git_source || data.value.status !== 'new'))
  const canStart = computed(() => !!data.value && ['stopped', 'crashed', 'failed'].includes(data.value.status) && !!dep.value)
  const canStop = computed(() => !!data.value && ['running', 'deploying'].includes(data.value.status))
  const busy = computed(() => actions.redeploy.isPending.value || actions.restart.isPending.value || actions.start.isPending.value || actions.stop.isPending.value)
  const shownStatus = computed(() => (actions.restart.isPending.value ? 'restarting' : actions.start.isPending.value ? 'starting' : (data.value?.status ?? '')))

  /**
   * The main button depends on where the code comes from: a linked repository
   * builds its newest commit, uploaded files get a new upload, a ready-made app
   * with a newer version gets that update. Anything else opens the app.
   */
  const primary = computed<'git' | 'upload' | 'update' | 'open' | 'none'>(() => {
    const a = data.value
    if (!a) return 'none'
    // A database or cache from a template: bitploy keeps its image up to date.
    if (a.template) return 'none'
    if (a.git_source) return 'git'
    if (a.catalog_id) return a.catalog_update ? 'update' : isWeb.value && dep.value ? 'open' : 'none'
    return 'upload'
  })

  function rebuild(onStarted?: () => void) {
    actions.redeploy.mutate(undefined, { onSuccess: () => onStarted?.() })
  }
  function restart() {
    actions.restart.mutate(undefined, { onSuccess: () => clearRestartNeeded(data.value?.id) })
  }

  // Changes that wait for a restart.
  const env = useQuery({
    queryKey: computed(() => keys.env(toValue(slug))),
    queryFn: () => api.env(toValue(slug)),
    enabled: computed(() => data.value?.status === 'running'),
    staleTime: 30_000,
  })
  const pending = useRestartNeeded(data, () => env.data.value)

  return { actions, isWeb, multi, dep, canRebuild, canStart, canStop, busy, shownStatus, primary, rebuild, restart, pending }
}
