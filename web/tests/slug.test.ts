import assert from 'node:assert/strict'
import { test } from 'node:test'
import { addressParts } from '../src/lib/slug.ts'

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
