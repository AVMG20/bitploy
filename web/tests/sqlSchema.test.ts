import assert from 'node:assert/strict'
import { test } from 'node:test'
import { foreignKeys } from '../src/lib/sqlSchema.ts'

test('column foreign keys', () => {
  const sql = 'CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, title TEXT)'
  assert.deepEqual(foreignKeys(sql), { user_id: { table: 'users', column: 'id' } })
})

test('table foreign keys, quoted names, constraint names, several columns', () => {
  const sql = `CREATE TABLE "order items" (
    order_id INT, sku TEXT, "qty" INT, -- a comment with REFERENCES nothing(x)
    CONSTRAINT fk_o FOREIGN KEY (order_id) REFERENCES "orders" ("id"),
    FOREIGN KEY (sku, qty) REFERENCES stock(sku, amount)
  )`
  assert.deepEqual(foreignKeys(sql), {
    order_id: { table: 'orders', column: 'id' },
    sku: { table: 'stock', column: 'sku' },
    qty: { table: 'stock', column: 'amount' },
  })
})

test('a reference without a column points at the primary key; no references, no keys', () => {
  assert.deepEqual(foreignKeys('CREATE TABLE a (b INTEGER REFERENCES c)'), { b: { table: 'c', column: '' } })
  assert.deepEqual(foreignKeys('CREATE TABLE a (b INTEGER, c TEXT DEFAULT \'references x(y)\')'), {})
  assert.deepEqual(foreignKeys('CREATE VIEW v AS SELECT 1'), {})
  assert.deepEqual(foreignKeys(null), {})
  assert.deepEqual(foreignKeys('CREATE TABLE broken (a INTEGER REFERENCES'), {})
})
