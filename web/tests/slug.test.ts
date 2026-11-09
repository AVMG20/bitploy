import assert from 'node:assert/strict'
import { test } from 'node:test'
import { addressError, addressParts, freeSlug } from '../src/lib/slug.ts'

test('@ is an address only where the domain itself is allowed', () => {
  // Someone who is not an admin: no change from the subdomain rule.
  assert.equal(addressError('@'), 'Use letters, numbers and -, not at the start or end.')
  // An admin on a domain that offers it, with spaces around.
  assert.equal(addressError(' @ ', true), undefined)
  // An admin on a domain that does not offer it gets the reason.
  assert.equal(addressError('@', false, 'example.com itself cannot be an app\'s address. Type a name.'), "example.com itself cannot be an app's address. Type a name.")
  // @ inside a name stays wrong everywhere.
  assert.equal(addressError('shop@', true), 'Use letters, numbers and -, not at the start or end.')
  assert.equal(addressError('@@', true), 'Use letters, numbers and -, not at the start or end.')
})

test('the subdomain rules of the Address field', () => {
  assert.equal(addressError('Shop-2'), undefined)
  assert.equal(addressError(''), 'Fill in a subdomain.')
  assert.equal(addressError('-shop'), 'Use letters, numbers and -, not at the start or end.')
  assert.equal(addressError('a'.repeat(41)), 'Use at most 40 characters.')
})

test('a taken address gets the first free number', () => {
  assert.equal(freeSlug('portfolio', new Set(['shop'])), 'portfolio')
  assert.equal(freeSlug('portfolio', new Set(['portfolio'])), 'portfolio-2')
  assert.equal(freeSlug('portfolio', ['portfolio', 'portfolio-2']), 'portfolio-3')
  assert.equal(freeSlug('', ['portfolio']), '')
  const long = 'a'.repeat(39) + 'b'
  const next = freeSlug(long, [long])
  assert.equal(next.length, 40)
  assert.ok(next.endsWith('-2'))
})

const me = {
  apps_domain: 'bitploy.nl',
  app_url_template: 'https://{slug}.bitploy.nl',
  app_domains: [
    { domain: 'bitploy.nl', default: true, url_template: 'https://{slug}.bitploy.nl' },
    { domain: 'example.com', default: false, url_template: 'https://{slug}.example.com' },
  ],
}

test('the address parts follow the chosen domain', () => {
  assert.deepEqual(addressParts(me), { before: 'https://', after: '.bitploy.nl' })
  assert.deepEqual(addressParts(me, 'example.com'), { before: 'https://', after: '.example.com' })
})

test('a domain the person can no longer pick still shows, with the port of the default', () => {
  const dev = { apps_domain: 'localhost', app_url_template: 'http://{slug}.localhost:8000', app_domains: [] }
  assert.deepEqual(addressParts(dev, 'old.test'), { before: 'http://', after: '.old.test:8000' })
})

test('without the account loaded there is a placeholder', () => {
  assert.deepEqual(addressParts(undefined), { before: 'https://', after: '.example.com' })
})
