import assert from 'node:assert/strict'
import { test } from 'node:test'
import { aboutService, imageParts, isMulti, listWords, serviceRole, serviceSource, sortServices } from '../src/lib/services.ts'

const svc = (name: string, kind: 'build' | 'image', extra: { image?: string; public?: boolean; port?: number | null } = {}) => ({
  name,
  kind,
  image: extra.image,
  public: extra.public ?? false,
  port: extra.port === undefined ? 8080 : extra.port,
})

test('imageParts reads repo and tag from any registry form', () => {
  assert.deepEqual(imageParts('postgres:16-alpine'), { repo: 'postgres', tag: '16-alpine' })
  assert.deepEqual(imageParts('docker.io/library/redis'), { repo: 'redis', tag: '' })
  assert.deepEqual(imageParts('ghcr.io/me/Redis:7@sha256:abc'), { repo: 'redis', tag: '7' })
  assert.deepEqual(imageParts(undefined), { repo: '', tag: '' })
})

test('serviceRole names what a service is for in plain words', () => {
  assert.equal(serviceRole(svc('web', 'build', { public: true })).label, 'Website')
  const db = serviceRole(svc('db', 'image', { image: 'postgres:16-alpine', port: 5432 }))
  assert.equal(db.key, 'database')
  assert.equal(db.product, 'PostgreSQL 16')
  assert.equal(serviceRole(svc('cache', 'image', { image: 'redis:7' })).key, 'cache')
  assert.equal(serviceRole(svc('api', 'build')).key, 'api')
  assert.equal(serviceRole(svc('jobs', 'build', { port: null })).key, 'worker')
  // A private nginx is just a service, not the website.
  assert.equal(serviceRole(svc('proxy', 'image', { image: 'nginx:1.27' })).key, 'service')
})

test('sortServices puts the website first and data last, keeping ties in order', () => {
  const list = [
    svc('postgres', 'image', { image: 'postgres:16' }),
    svc('worker', 'build', { port: null }),
    svc('web', 'build', { public: true }),
    svc('api', 'build'),
  ]
  assert.deepEqual(
    sortServices(list).map((s) => s.name),
    ['web', 'api', 'worker', 'postgres'],
  )
})

test('isMulti: several services or the multi layout', () => {
  assert.equal(isMulti(undefined), false)
  assert.equal(isMulti({ services: [], service_layout: 'single' } as never), false)
  assert.equal(isMulti({ services: [{}, {}], service_layout: 'single' } as never), true)
  assert.equal(isMulti({ services: [{}], service_layout: 'multi' } as never), true)
})

test('listWords joins names the way a sentence does', () => {
  assert.equal(listWords([]), '')
  assert.equal(listWords(['a']), 'a')
  assert.equal(listWords(['a', 'b']), 'a and b')
  assert.equal(listWords(['a', 'b', 'c']), 'a, b and c')
})

test('serviceSource shows the image or the folder it is built from', () => {
  assert.equal(serviceSource({ ...svc('db', 'image', { image: 'postgres:16' }) }), 'postgres:16')
  assert.equal(serviceSource({ ...svc('web', 'build'), build: '.' }), 'Built from your code')
  assert.equal(serviceSource({ ...svc('api', 'build'), build: { context: './api' } }), 'Built from api/')
})

test('aboutService names the service once', () => {
  assert.equal(aboutService('It did not answer.', 'api'), 'The api service: It did not answer.')
  assert.equal(aboutService('The service api did not answer.', 'api'), 'The service api did not answer.')
  assert.equal(aboutService('It did not answer.', undefined), 'It did not answer.')
})
