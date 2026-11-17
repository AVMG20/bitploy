import { computed, ref, toValue } from 'vue'
import type { MaybeRefOrGetter } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { keys, useApp } from '@/composables/queries'
import { api } from '@/lib/api'
import { versionLabel } from '@/lib/format'
import { toastError, toastLoading } from '@/lib/notify'
import type { Deployment } from '@/lib/types'

/**
 * Going back to an earlier version, for every page that lists deployments
 * (the Deployments tab and the Overview): which versions may be picked, the
 * mutation, and the words for the confirmation (`target` is the version asked about).
 */
export function useRollback(slug: MaybeRefOrGetter<string>) {
  const qc = useQueryClient()
  const app = useApp(() => toValue(slug))

  const target = ref<Deployment | null>(null)
  const confirmOpen = computed({
    get: () => target.value !== null,
    set: (v) => {
      if (!v) target.value = null
    },
  })

  const rollback = useMutation({
    mutationFn: (d: Deployment) => api.rollback(toValue(slug), d.id),
    onMutate: (d) => {
      target.value = null
      return toastLoading(`Going back to ${versionLabel(d.seq).toLowerCase()}...`)
    },
    onSuccess: (_r, d, t) => {
      t.ok(
        `Going back to ${versionLabel(d.seq).toLowerCase()}`,
        app.data.value?.volume ? 'The app is offline for a few seconds while it switches.' : undefined,
      )
      void qc.invalidateQueries({ queryKey: keys.deployments(toValue(slug)) })
      void qc.invalidateQueries({ queryKey: keys.app(toValue(slug)) })
      void qc.invalidateQueries({ queryKey: keys.apps })
    },
    onError: (e, _d, t) => (t ? t.fail(e) : toastError(e)),
  })

  // What going back does, and that a push to the linked branch builds the newest code again.
  const goBackText = computed(() => {
    const gs = app.data.value?.git_source
    let text = `Your app runs ${versionLabel(target.value?.seq).toLowerCase()} again.`
    // Migrations are not undone: the older version must work with the database as it is now.
    if (app.data.value?.before_deploy_command || app.data.value?.after_deploy_command)
      text += ' Your deploy commands do not run when you go back, so changes they made, like database migrations, stay.'
    if (gs?.auto_deploy) text += ` The next push to ${gs.branch} on GitHub builds and deploys your newest code again.`
    return text
  })

  function canRollback(d: Deployment): boolean {
    const built = !!d.image || !!d.services?.some((s) => !!s.image)
    return d.state === 'superseded' && built && !d.image_removed && d.id !== app.data.value?.current_deployment?.id
  }

  return { target, confirmOpen, rollback, goBackText, canRollback }
}
