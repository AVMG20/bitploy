// Names and colours of the disk page (Admin > Server > Disk).

import type { DiskCleanupKind, DiskContainerItem, DiskFilesKey, DiskImageCategory, DiskSegmentKey, DiskVolumeItem } from './types'

/** The parts of the used space, in the order of the bar. Colours are theme tokens only. */
export const SEGMENTS: Record<DiskSegmentKey, { label: string; color: string }> = {
  images_in_use: { label: 'Images in use', color: 'var(--chart-1)' },
  images_rollback: { label: 'Rollback versions', color: 'color-mix(in oklch, var(--chart-1) 55%, transparent)' },
  images_unused: { label: 'Unused images', color: 'var(--chart-3)' },
  images_other: { label: 'Images not from bitploy', color: 'color-mix(in oklch, var(--chart-3) 50%, transparent)' },
  build_cache: { label: 'Build cache', color: 'var(--chart-5)' },
  containers: { label: 'Container files', color: 'color-mix(in oklch, var(--chart-5) 50%, transparent)' },
  volumes_app: { label: 'App volumes', color: 'var(--chart-2)' },
  volumes_other: { label: 'Other volumes', color: 'color-mix(in oklch, var(--chart-2) 50%, transparent)' },
  bitploy_files: { label: 'bitploy files', color: 'var(--chart-4)' },
  other: { label: 'Rest of the system', color: 'color-mix(in oklch, var(--muted-foreground) 35%, transparent)' },
}

export const IMAGE_CATEGORIES: Record<DiskImageCategory, string> = {
  in_use: 'In use',
  rollback: 'Rollback',
  unused: 'Unused',
  other: 'Not bitploy’s',
}

export const CONTAINER_CATEGORIES: Record<DiskContainerItem['category'], string> = {
  running: 'Running',
  stopped: 'Stopped app',
  leftover: 'Leftover',
  other: 'Not bitploy’s',
}

export const VOLUME_CATEGORIES: Record<DiskVolumeItem['category'], string> = {
  app: 'App data',
  builder: 'Build cache',
  orphan: 'Deleted app',
  other: 'Not bitploy’s',
}

export const FILES: Record<DiskFilesKey, string> = {
  database: 'bitploy’s database',
  database_backups: 'Backups of bitploy’s database',
  app_backups: 'App backups',
  sources: 'Build sources',
  uploads: 'Uploads',
  builds: 'Build files and logs',
  proxy: 'Proxy certificates and config',
  other: 'Other',
}

export interface CleanupInfo {
  title: string
  /** What goes, and what that means afterwards. */
  description: string
  /** What the confirm dialog says will happen, given the size and count. */
  confirm: (size: string, count: number) => string
  confirmLabel: string
  destructive: boolean
  /** "3 images" for count 3; empty for build cache. */
  unit: (n: number) => string
}

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

export const CLEANUPS: Record<DiskCleanupKind, CleanupInfo> = {
  build_cache: {
    title: 'Build cache',
    description: 'Layers saved to speed up builds. The next build of each app starts from scratch.',
    confirm: (size) => `Deletes about ${size} of build cache, from bitploy’s builder and from Docker. The next build of each app takes longer. Apps that run are not affected.`,
    confirmLabel: 'Delete build cache',
    destructive: false,
    unit: () => '',
  },
  unused_images: {
    title: 'Unused images',
    description: 'bitploy images no app version uses: failed builds and images of deleted apps.',
    confirm: (size, n) => `Deletes ${plural(n, 'image', 'images')} (about ${size}) that no app version uses. Apps and their rollback versions keep their images.`,
    confirmLabel: 'Delete unused images',
    destructive: false,
    unit: (n) => plural(n, 'image', 'images'),
  },
  stopped_containers: {
    title: 'Leftover containers',
    description: 'Stopped containers of replaced versions and deleted apps. Stopped apps keep theirs.',
    confirm: (size, n) => `Deletes ${plural(n, 'stopped container', 'stopped containers')} (about ${size}) that no app runs or can start again. Their volumes stay.`,
    confirmLabel: 'Delete containers',
    destructive: false,
    unit: (n) => plural(n, 'container', 'containers'),
  },
  rollback_images: {
    title: 'Rollback versions',
    description: 'Images of older app versions. Each app keeps the version it runs.',
    confirm: (size, n) =>
      `Deletes ${plural(n, 'image', 'images')} of older app versions (about ${size}). After this, apps cannot roll back to those versions. Each app keeps the version it runs.`,
    confirmLabel: 'Delete rollback versions',
    destructive: true,
    unit: (n) => plural(n, 'image', 'images'),
  },
  other_images: {
    title: 'Images not from bitploy',
    description: 'Unused images other software on this server pulled or built.',
    confirm: (size, n) =>
      `Deletes ${plural(n, 'image', 'images')} (about ${size}) that bitploy did not make and no container uses. Software that needs one pulls or builds it again.`,
    confirmLabel: 'Delete images',
    destructive: true,
    unit: (n) => plural(n, 'image', 'images'),
  },
  orphan_volumes: {
    title: 'Volumes of deleted apps',
    description: 'Saved files and database data of apps that no longer exist.',
    confirm: (size, n) => `Deletes ${plural(n, 'volume', 'volumes')} (about ${size}) with the saved files and database data of deleted apps. This cannot be undone.`,
    confirmLabel: 'Delete volumes',
    destructive: true,
    unit: (n) => plural(n, 'volume', 'volumes'),
  },
}

/** The steps of the automatic cleanup, as a list in a sentence. */
export const STEP_NAMES: Record<DiskCleanupKind, string> = {
  build_cache: 'build cache',
  unused_images: 'unused images',
  stopped_containers: 'leftover containers',
  rollback_images: 'rollback versions',
  other_images: 'images not from bitploy',
  orphan_volumes: 'volumes of deleted apps',
}

/** Percent of total, 0 when the total is unknown. */
export function share(bytes: number, total: number): number {
  return total > 0 ? Math.max(0, Math.min(100, (bytes / total) * 100)) : 0
}
