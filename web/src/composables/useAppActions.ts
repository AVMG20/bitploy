import { toValue } from 'vue'
import type { MaybeRefOrGetter } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import { keys } from './queries'

/** Redeploy / restart / stop / start for one app, with toasts and cache refresh. */
export function useAppActions(slug: MaybeRefOrGetter<string>) {
  const qc = useQueryClient()

  function refresh() {
    const s = toValue(slug)
    void qc.invalidateQueries({ queryKey: keys.app(s) })
    void qc.invalidateQueries({ queryKey: keys.deployments(s) })
    void qc.invalidateQueries({ queryKey: keys.apps })
  }

  function action(fn: (s: string) => Promise<unknown>, done: string, detail?: string) {
    return useMutation({
      mutationFn: () => fn(toValue(slug)),
      onSuccess: () => {
        toastOk(done, detail)
        refresh()
      },
      onError: (e) => toastError(e),
    })
  }

  // Stopping a build is safe (the live version keeps running), so there is no confirmation.
  // If it is too late or the build already ended, the server says so and the lists catch up.
  const cancel = useMutation({
    mutationFn: (deploymentId: string) => api.cancelDeployment(deploymentId),
    onSuccess: () => {
      toastOk('Build cancelled')
      refresh()
    },
    onError: (e) => {
      toastError(e)
      refresh()
    },
  })

  return {
    cancel,
    redeploy: action(api.redeploy, 'Redeploy started', 'A live version keeps running until the new one is ready.'),
    // Restart and start answer once the app runs again.
    restart: action(api.restart, 'App restarted', 'It is running again with a fresh start.'),
    stop: action(api.stop, 'App stopped', 'Its address now shows a "stopped" page. Start it again any time.'),
    start: action(api.start, 'App started'),
  }
}
