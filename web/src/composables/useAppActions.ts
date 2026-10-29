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

  return {
    redeploy: action(api.redeploy, 'Redeploy started', 'Your app keeps running until the new version is ready.'),
    restart: action(api.restart, 'Restarting your app'),
    stop: action(api.stop, 'App stopped', 'Its address now shows a "stopped" page. Start it again any time.'),
    start: action(api.start, 'Starting your app'),
  }
}
