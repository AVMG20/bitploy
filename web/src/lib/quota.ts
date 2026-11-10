import type { ComposeCheck, Quota, Usage } from './types'

// What is left of a person's allowance, and whether something new fits in it.
// The same rule as the server (internal/app quota checks): memory and CPU are
// not checked for a person allowed to overallocate, storage always is, and a
// storage allowance of 0 means no limit.

type MeLike = { quota: Quota; usage: Usage } | undefined | null

/** Memory left in MB; null when it is not checked (overallocating) or not known yet. */
export function memoryLeftMB(me: MeLike): number | null {
  if (!me || me.quota.overallocate) return null
  return Math.max(0, me.quota.memory_mb - me.usage.memory_reserved_mb)
}

/** CPU left (1000 = one core); null when it is not checked or not known yet. */
export function cpuLeftMillis(me: MeLike): number | null {
  if (!me || me.quota.overallocate) return null
  return Math.max(0, me.quota.cpu_millis - me.usage.cpu_reserved_millis)
}

/** Storage left in MB, shared by saved files, images and backups; null when there is no limit. */
export function storageLeftMB(me: MeLike): number | null {
  if (!me || me.quota.volume_mb <= 0) return null
  return Math.max(0, me.quota.volume_mb - me.usage.files_mb - me.usage.backups_mb - me.usage.volume_reserved_mb)
}

/**
 * An admin gave the account no apps of its own (apps 0, like an account made
 * with a project invite while those may not deploy): it can only add apps to
 * projects shared with it, which count against their owner.
 */
export function noOwnApps(me: MeLike): boolean {
  return !!me && me.quota.apps <= 0
}

/** What a new app page says to an account with no apps of its own. */
export const NO_OWN_APPS = "Your account can't create apps of its own. Ask an admin to raise your limits."

/** The account has as many apps as it may have. */
export function appsFull(me: MeLike): boolean {
  return !!me && me.usage.apps >= me.quota.apps
}

export interface Need {
  memory_mb: number
  cpu_millis?: number
  volume_mb?: number
}

export interface Fit {
  memoryLeft: number | null
  cpuLeft: number | null
  storageLeft: number | null
  memoryOver: boolean
  cpuOver: boolean
  storageOver: boolean
  /** Nothing is over: adding or deploying it is allowed. */
  fits: boolean
}

/** Whether something new fits in what is left of the allowance. */
export function fitAllowance(need: Need, me: MeLike): Fit {
  const memoryLeft = memoryLeftMB(me)
  const cpuLeft = cpuLeftMillis(me)
  const storageLeft = storageLeftMB(me)
  const memoryOver = memoryLeft !== null && need.memory_mb > memoryLeft
  const cpuOver = cpuLeft !== null && (need.cpu_millis ?? 0) > cpuLeft
  const storageOver = storageLeft !== null && (need.volume_mb ?? 0) > storageLeft
  return { memoryLeft, cpuLeft, storageLeft, memoryOver, cpuOver, storageOver, fits: !memoryOver && !cpuOver && !storageOver }
}

/**
 * Whether a checked compose file fits. The server's answer wins when it gave
 * one (it leaves out the app the deployment replaces); otherwise the totals
 * are compared with the account's own numbers.
 */
export function composeFit(check: ComposeCheck, me: MeLike): Fit & { message: string } {
  const services = check.services ?? []
  const volumes = check.volumes ?? []
  const need: Need = {
    memory_mb: check.memory_mb_total ?? services.reduce((n, s) => n + s.memory_mb, 0),
    cpu_millis: check.cpu_millis_total ?? services.reduce((n, s) => n + s.cpu_millis, 0),
    volume_mb: check.volume_mb_total ?? volumes.reduce((n, v) => n + v.size_mb, 0),
  }
  const q = check.quota
  if (!q) return { ...fitAllowance(need, me), message: '' }
  const unchecked = !!me?.quota.overallocate
  const memoryLeft = unchecked ? null : q.memory_mb_left
  const cpuLeft = unchecked ? null : q.cpu_millis_left
  const storageLeft = q.storage_mb_left
  const memoryOver = memoryLeft !== null && need.memory_mb > memoryLeft
  const cpuOver = cpuLeft !== null && (need.cpu_millis ?? 0) > cpuLeft
  const storageOver = storageLeft !== null && (need.volume_mb ?? 0) > storageLeft
  return { memoryLeft, cpuLeft, storageLeft, memoryOver, cpuOver, storageOver, fits: q.fits, message: q.fits ? '' : q.message }
}

/** MB as GB with two decimals: how the admin's limits forms show memory and storage. */
export const mbToGB = (mb: number) => Math.round((mb / 1024) * 100) / 100

/**
 * MB for a limit entered in GB. A value left as the form showed it keeps its
 * exact MB: 640 MB shows as 0.63 GB, which would come back as 645 MB.
 */
export function gbToMB(gb: number, wasMB: number): number {
  return gb === mbToGB(wasMB) ? wasMB : Math.round(gb * 1024)
}
