import { api } from './api'
import { HISTORY_KEY_PREFIX } from './dbvalue'
import { keys } from './live'
import type { DbKey, DbRow, DbRows, DbRowsQuery, DbSchema, DbSqlResponse, DbValue } from './types'

type Row = Record<string, DbValue>

/** What changes data. Only a database that can be changed has it. */
export interface DbWrite {
  insert: (table: string, values: Row) => Promise<{ key: DbKey | null; row: DbRow }>
  update: (table: string, key: DbKey, values: Row, match?: Row) => Promise<{ row: DbRow }>
  updateMany: (table: string, keys: DbKey[], values: Row, match?: Row[]) => Promise<{ updated: number }>
  remove: (table: string, keys: DbKey[], match?: Row[]) => Promise<{ deleted: number }>
}

/**
 * Where the database viewer reads from. The viewer's pages and components only know this, so the
 * same UI shows a SQLite file in an app's /data (which can be edited) and bitploy's own database
 * (which cannot).
 */
export interface DbSource {
  kind: 'app' | 'admin'
  /** Prefix of the cached schema query, and of the cached rows queries: used to refresh them. */
  schemaKey: readonly unknown[]
  rowsKey: readonly unknown[]
  /** Where the SQL console keeps its history in the browser. */
  historyKey: string
  schema: () => Promise<DbSchema>
  rows: (q: DbRowsQuery) => Promise<DbRows>
  sql: (sql: string, write: boolean) => Promise<DbSqlResponse>
  /** The whole value of one cell (a text cut in the grid), up to 4 MiB. Null: not offered. */
  cell: ((table: string, key: DbKey, column: string) => Promise<DbValue>) | null
  /** Null: this database is read-only for good (no switch, no editing, no writes in the console). */
  write: DbWrite | null
}

/** A SQLite file in an app's /data. */
export function appDbSource(slug: string, path: string): DbSource {
  return {
    kind: 'app',
    schemaKey: keys.dbSchema(slug, path),
    rowsKey: keys.dbRows(slug, path),
    historyKey: `${HISTORY_KEY_PREFIX}${slug}`,
    schema: () => api.dbSchema(slug, path),
    rows: (q) => api.dbRows(slug, path, q),
    sql: (sql, write) => api.dbSql(slug, path, sql, write),
    cell: async (table, key, column) => (await api.dbCell(slug, path, table, key, column)).value,
    write: {
      insert: (table, values) => api.dbInsert(slug, path, table, values),
      update: (table, key, values, match) => api.dbUpdate(slug, path, table, key, values, match),
      updateMany: (table, ks, values, match) => api.dbUpdateMany(slug, path, table, ks, values, match),
      remove: (table, ks, match) => api.dbDelete(slug, path, table, ks, match),
    },
  }
}

/** bitploy's own database, for admins: read-only, secrets hidden by the server. */
export const adminDbSource: DbSource = {
  kind: 'admin',
  schemaKey: keys.adminDbSchema,
  rowsKey: keys.adminDbRows,
  historyKey: `${HISTORY_KEY_PREFIX}admin`,
  schema: () => api.adminDbSchema(),
  rows: (q) => api.adminDbRows(q),
  sql: (sql) => api.adminDbSql(sql),
  cell: null,
  write: null,
}
