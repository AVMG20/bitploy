/** The tabs of the Admin page; "database" is a page of its own (/admin/database) under the same tab bar. */
export type AdminTab = 'users' | 'invites' | 'signups' | 'server' | 'jobs' | 'apps' | 'domains' | 'github' | 'audit' | 'database'

/** The tabs AdminPage renders itself, picked with ?tab=. */
export const ADMIN_PAGE_TABS: AdminTab[] = ['users', 'invites', 'signups', 'server', 'jobs', 'apps', 'domains', 'github', 'audit']
