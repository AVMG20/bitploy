import type { App, Project } from './types'

/** What one app takes of its owner's storage allowance, in MB. */
export interface AppStorage {
  app: App
  /** Kept images (versions for a rollback) and stored source; null until measured. */
  filesMB: number | null
  backupsMB: number
  /** What /data holds. */
  volumeUsedMB: number
  /** The size of /data; 0 without a volume. */
  volumeMB: number
  /** Files, backups and what /data holds. */
  usedMB: number
}

/**
 * The storage of every app the user owns, biggest first. Apps in a project
 * someone shared with them count towards that owner's allowance, so they are
 * left out. Same sums as in use on the Storage card on the home page.
 */
export function storageByApp(apps: readonly App[], ownerId: string): AppStorage[] {
  return apps
    .filter((a) => a.owner_id === ownerId)
    .map((app) => {
      const files = app.files_mb ?? 0
      const volumeMB = app.volume ? app.volume_mb : 0
      const volumeUsedMB = app.volume ? (app.volume_used_mb ?? 0) : 0
      return {
        app,
        filesMB: app.files_mb,
        backupsMB: app.backups_mb,
        volumeUsedMB,
        volumeMB,
        usedMB: files + app.backups_mb + volumeUsedMB,
      }
    })
    .sort((a, b) => b.usedMB - a.usedMB || (a.app.name || a.app.slug).localeCompare(b.app.name || b.app.slug))
}

/** A project and what its apps take, so a postgres shows under the project it belongs to. */
export interface ProjectStorage {
  project: Project
  /** Its apps, biggest first. */
  apps: AppStorage[]
  filesMB: number
  backupsMB: number
  volumeUsedMB: number
  volumeMB: number
  usedMB: number
}

/** The storage of the user's own apps per project, biggest project first; projects without an app of theirs are left out. */
export function storageByProject(projects: readonly Project[], ownerId: string): ProjectStorage[] {
  return projects
    .map((project) => {
      const apps = storageByApp(project.apps, ownerId)
      const sum = (f: (a: AppStorage) => number) => apps.reduce((n, a) => n + f(a), 0)
      return {
        project,
        apps,
        filesMB: sum((a) => a.filesMB ?? 0),
        backupsMB: sum((a) => a.backupsMB),
        volumeUsedMB: sum((a) => a.volumeUsedMB),
        volumeMB: sum((a) => a.volumeMB),
        usedMB: sum((a) => a.usedMB),
      }
    })
    .filter((p) => p.apps.length > 0)
    .sort((a, b) => b.usedMB - a.usedMB || a.project.name.localeCompare(b.project.name))
}
