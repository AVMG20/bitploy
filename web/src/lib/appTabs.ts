/** The tabs of an app's page, in order. Shared by the page and the quick switcher. */
export interface AppTab {
  name: string
  label: string
  /** Only for apps with a web address (not background workers). */
  webOnly?: boolean
  /** Only when the app's saved files hold a SQLite database. */
  database?: boolean
}

export const APP_TABS: AppTab[] = [
  { name: 'app', label: 'Overview' },
  { name: 'app-deployments', label: 'Deployments' },
  { name: 'app-logs', label: 'Logs' },
  { name: 'app-traffic', label: 'Traffic', webOnly: true },
  { name: 'app-variables', label: 'Variables' },
  { name: 'app-domains', label: 'Domains', webOnly: true },
  { name: 'app-storage', label: 'Storage' },
  { name: 'app-database-default', label: 'Database', database: true },
  { name: 'app-settings', label: 'Settings' },
]
