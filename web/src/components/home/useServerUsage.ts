import { computed } from 'vue'
import { useMe } from '@/composables/queries'
import { formatCPUMillis, formatMB } from '@/lib/format'
import { usageBar } from '@/lib/usage'
import type { UsageBar } from '@/lib/usage'

// What the apps take of the account's limits, worked out once for the Home
// page's server card and its attention list, so both say the same thing.

export interface Attention {
  key: string
  tone: 'warn' | 'bad'
  title: string
  text: string
  /** The storage card can open the per-app storage dialog. */
  action?: 'storage'
}

export function useServerUsage() {
  const me = useMe()
  const usage = computed(() => me.data.value?.usage)
  const quota = computed(() => me.data.value?.quota)
  const isAdmin = computed(() => me.data.value?.user.role === 'admin')
  // An admin can let someone use more memory and CPU than the limits: nothing is checked there.
  const unlimited = computed(() => !!quota.value?.overallocate)
  // Storage allocated counts each saved-files volume at its full size (as the server checks it); in use counts what is in it.
  const storageTaken = computed(() => (usage.value ? usage.value.files_mb + usage.value.backups_mb + usage.value.volume_reserved_mb : 0))
  const storageUsed = computed(() => (usage.value ? usage.value.files_mb + usage.value.backups_mb + usage.value.volume_used_mb : 0))

  /** What to do when over a limit: an admin raises their own, everyone else asks. */
  function overText(what: string, fix: string): string {
    return `Over your ${what} limit. ${fix}, or ${isAdmin.value ? 'raise your limit in Admin, People' : 'ask your admin for more'}.`
  }
  const memAlert = computed(() =>
    usage.value && quota.value && !unlimited.value && usage.value.memory_reserved_mb > quota.value.memory_mb
      ? overText('memory', 'New deploys that need more memory fail. Lower the memory of an app or stop one')
      : '',
  )
  const cpuAlert = computed(() =>
    usage.value && quota.value && !unlimited.value && usage.value.cpu_reserved_millis > quota.value.cpu_millis
      ? overText('CPU', 'New deploys that need more CPU fail. Lower the CPU of an app or stop one')
      : '',
  )
  const storageAlert = computed(() =>
    usage.value && quota.value && quota.value.volume_mb > 0 && storageTaken.value > quota.value.volume_mb ? overText('storage', 'Delete old backups or saved files') : '',
  )
  const appsAlert = computed(() =>
    usage.value && quota.value && usage.value.apps > quota.value.apps ? overText('apps', 'Delete the apps you no longer need before adding one') : '',
  )

  // Things that need a person on the Home page: over a limit (red) or close to it (amber).
  const attention = computed<Attention[]>(() => {
    const u = usage.value
    const q = quota.value
    if (!u || !q) return []
    const out: Attention[] = []
    const check = (key: string, label: string, alert: string, bar: UsageBar, near: string, action?: 'storage') => {
      if (alert) out.push({ key, tone: 'bad', title: `Over your ${label} limit`, text: alert, action })
      else if (bar.level === 'warn' || bar.level === 'full') out.push({ key, tone: 'warn', title: `${label[0]!.toUpperCase()}${label.slice(1)} almost full`, text: near, action })
    }
    check('memory', 'memory', memAlert.value, usageBar(u.memory_used_mb, u.memory_reserved_mb, unlimited.value ? null : q.memory_mb), `Your apps reserve ${formatMB(u.memory_reserved_mb)} of ${formatMB(q.memory_mb)}. Another big app or database may not fit.`)
    check('cpu', 'CPU', cpuAlert.value, usageBar(0, u.cpu_reserved_millis, unlimited.value ? null : q.cpu_millis), `Your apps reserve ${formatCPUMillis(u.cpu_reserved_millis)} of ${formatCPUMillis(q.cpu_millis)}.`)
    check('storage', 'storage', storageAlert.value, usageBar(storageUsed.value, storageTaken.value, q.volume_mb), `Files, backups and saved data take ${formatMB(storageTaken.value)} of ${formatMB(q.volume_mb)}.`, 'storage')
    check('apps', 'apps', appsAlert.value, usageBar(u.apps_running, u.apps, q.apps), `You have ${u.apps} of ${q.apps} apps. Databases count too.`)
    return out
  })

  return { me, usage, quota, isAdmin, unlimited, storageTaken, storageUsed, memAlert, cpuAlert, storageAlert, appsAlert, attention }
}
