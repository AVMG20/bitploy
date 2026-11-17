import { Globe, HardDrive, KeyRound, SlidersHorizontal } from 'lucide-vue-next'
import type { Component } from 'vue'

/** One page of an app. Shared by the page and the quick switcher. */
export interface AppTab {
  name: string
  label: string
  /** Only for apps with a web address (not background workers). */
  webOnly?: boolean
  /** Only when the app's saved files hold a SQLite database. */
  database?: boolean
  /** Only when the viewer may open a shell in the app's container (terminalOffered). */
  terminal?: boolean
}

/** Every page of an app, for the quick switcher. The tab bar shows MAIN_TABS. */
export const APP_TABS: AppTab[] = [
  { name: 'app', label: 'Overview' },
  { name: 'app-deployments', label: 'Deployments' },
  { name: 'app-logs', label: 'Logs' },
  { name: 'app-terminal', label: 'Terminal', terminal: true },
  { name: 'app-traffic', label: 'Metrics' },
  { name: 'app-variables', label: 'Variables' },
  { name: 'app-domains', label: 'Domains', webOnly: true },
  { name: 'app-storage', label: 'Storage' },
  { name: 'app-database-default', label: 'Database', database: true },
  { name: 'app-settings', label: 'Settings' },
]

/** The tab bar: four to look at, one to change things. Configure opens General. */
export const MAIN_TABS: { name: string; label: string; to: string }[] = [
  { name: 'app', label: 'Overview', to: 'app' },
  { name: 'app-deployments', label: 'Deployments', to: 'app-deployments' },
  { name: 'app-logs', label: 'Logs', to: 'app-logs' },
  { name: 'app-traffic', label: 'Metrics', to: 'app-traffic' },
  { name: 'configure', label: 'Configure', to: 'app-settings' },
]

export interface ConfigSection extends AppTab {
  icon: Component
}

/** The side menu of Configure. Each entry is a page of its own that keeps the Configure tab lit. */
export const CONFIG_SECTIONS: ConfigSection[] = [
  { name: 'app-settings', label: 'General', icon: SlidersHorizontal },
  { name: 'app-variables', label: 'Variables', icon: KeyRound },
  { name: 'app-domains', label: 'Domains', icon: Globe, webOnly: true },
  { name: 'app-storage', label: 'Storage & backups', icon: HardDrive },
]

/** Pages that belong under the Configure tab. The database viewer is a page of its own, opened from the Overview. */
const CONFIG_NAMES = new Set(['app-settings', 'app-variables', 'app-domains', 'app-storage'])
export const isConfigRoute = (name: unknown): boolean => CONFIG_NAMES.has(String(name ?? ''))
