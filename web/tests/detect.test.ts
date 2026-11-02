// Run with `npm test` (Node 22.18 or newer runs the TypeScript directly).
// detectSource mirrors deploy.PlanBuild (internal/deploy/buildmode.go).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { detectSource } from '../src/lib/detect.ts'
import { listZipNames, readZipText, zipNameAtTop } from '../src/lib/zip.ts'

const files = ['package.json', 'package-lock.json', 'src/main.js']
const vite = JSON.stringify({ scripts: { build: 'vite build' }, devDependencies: { vite: '^5' } })
const start = JSON.stringify({ scripts: { start: 'node server.js' } })
const neither = JSON.stringify({ scripts: { test: 'jest' } })

const detect = (paths: string[], pkg: string | null, mode: 'auto' | 'static' | 'dockerfile' = 'auto') => detectSource(paths, mode, '', 'Dockerfile', pkg)

test('vite with a build script and index.html is a Vite site', () => {
  assert.deepEqual(detect([...files, 'index.html'], vite), { kind: 'template', label: 'Vite site', known: true })
})

test('vite without index.html at the top is not a site (SvelteKit, Remix)', () => {
  assert.equal(detect(files, vite).kind, 'none')
  const remix = JSON.stringify({ scripts: { build: 'remix vite:build', start: 'remix-serve build/server/index.js' }, devDependencies: { vite: '^7' } })
  assert.deepEqual(detect(files, remix), { kind: 'template', label: 'Node.js app', known: true, build: true })
})

test('a start script is a Node.js app', () => {
  assert.deepEqual(detect(files, start), { kind: 'template', label: 'Node.js app', known: true, build: false })
})

test('Laravel and Django are not Node projects, as on the server', () => {
  const laravel = JSON.stringify({ scripts: { build: 'vite build' }, devDependencies: { vite: '^7' } })
  assert.equal(detect(['artisan', 'composer.json', 'index.html', ...files], laravel).kind, 'none')
  assert.equal(detect(['manage.py', 'requirements.txt', ...files], start).kind, 'none')
})

test('neither script is still a dead end, with the assistant line', () => {
  const d = detect(files, neither)
  assert.equal(d.kind, 'none')
  assert.equal(d.kind === 'none' && d.label, 'Nothing to build')
  assert.equal(d.kind === 'none' && d.assistant, true)
})

test('a Node project with a page at the top that cannot be run says it needs a build', () => {
  const d = detect([...files, 'index.html'], neither)
  assert.equal(d.kind === 'none' && d.label, 'Needs a build first')
  assert.equal(d.kind === 'none' && d.assistant, true)
})

test('malformed package.json is a dead end', () => {
  assert.equal(detect(files, '{"scripts": {').kind, 'none')
})

test('package.json that was not read gives a neutral preview', () => {
  assert.deepEqual(detect(files, null), { kind: 'template', label: 'Node.js project', known: false })
})

test('a Dockerfile wins', () => {
  assert.deepEqual(detect(['Dockerfile', ...files], vite), { kind: 'dockerfile', label: 'Dockerfile' })
})

test('dockerfile mode is unaffected', () => {
  const d = detect(files, vite, 'dockerfile')
  assert.equal(d.kind === 'none' && d.label, 'No Dockerfile found')
  assert.equal(d.kind === 'none' && d.assistant, undefined)
})

test('go.mod wins over package.json, as on the server', () => {
  assert.equal(detect(['go.mod', ...files], start).kind, 'none')
})

test('plain websites are unchanged', () => {
  assert.equal(detect(['index.html', 'css/site.css'], null).kind, 'static')
  assert.equal(detect(['README.md'], null).kind, 'none')
})

test('package.json is read from a zip', async () => {
  const body = new TextEncoder().encode(start)
  const stored = zipOf('my-site/package.json', body)
  const names = (await listZipNames(stored))!
  assert.deepEqual(names, ['my-site/package.json'])
  assert.equal(zipNameAtTop(names, 'package.json'), 'my-site/package.json')
  assert.equal(await readZipText(stored, 'my-site/package.json'), start)
  assert.equal(await readZipText(stored, 'nope'), null)
  assert.equal(await readZipText(stored, 'my-site/package.json', 4), null)
})

test('package.json is read from a deflated zip (made with Info-ZIP)', async () => {
  // zip -r -X t.zip my-app: my-app/, my-app/index.html and my-app/package.json, both deflated.
  const zip = new Blob([Buffer.from('UEsDBAoAAAAAAGNkYl0AAAAAAAAAAAAAAAAHAAAAbXktYXBwL1BLAwQUAAAACABjZGJdkPjiNB0AAAAgAAAAEQAAAG15LWFwcC9pbmRleC5odG1ss1FMyU8uqSxIVcgoyc2xsynJLMlJtauw0YcwuABQSwMEFAAAAAgAY2RiXS/ZRziBAAAAzwAAABMAAABteS1hcHAvcGFja2FnZS5qc29uTY1BCoMwEEX3OUXI2ohdCV33HAVrBh0w08HEFBHv3kkkpcv//ps/h9La0ODB3LXxux2YTZNZGFfkGAQfEgW8NlxcthJG0FdqrspBqkVFvEJC+Pz8mqU9y77cPICBHNCI8PenrMjZs2+7tqt7mVpetgnJhreHOCNNRbsVLe+qU30BUEsBAh4DCgAAAAAAY2RiXQAAAAAAAAAAAAAAAAcAAAAAAAAAAAAQAO1BAAAAAG15LWFwcC9QSwECHgMUAAAACABjZGJdkPjiNB0AAAAgAAAAEQAAAAAAAAABAAAApIElAAAAbXktYXBwL2luZGV4Lmh0bWxQSwECHgMUAAAACABjZGJdL9lHOIEAAADPAAAAEwAAAAAAAAABAAAApIFxAAAAbXktYXBwL3BhY2thZ2UuanNvblBLBQYAAAAAAwADALUAAAAjAQAAAAA=', 'base64')])
  const names = (await listZipNames(zip))!
  assert.deepEqual(names, ['my-app/', 'my-app/index.html', 'my-app/package.json'])
  const name = zipNameAtTop(names, 'package.json')
  assert.equal(name, 'my-app/package.json')
  const text = await readZipText(zip, name!)
  assert.equal(text?.length, 207)
  assert.deepEqual(JSON.parse(text!).devDependencies, { vite: '^7.0.0', 'vite-plugin-something': '^1.0.0' })
  assert.equal(await readZipText(zip, name!, 100), null)
})

// A zip with one stored (uncompressed) file.
function zipOf(name: string, data: Uint8Array): Blob {
  const n = new TextEncoder().encode(name)
  const local = new DataView(new ArrayBuffer(30))
  local.setUint32(0, 0x04034b50, true)
  local.setUint32(18, data.length, true)
  local.setUint32(22, data.length, true)
  local.setUint16(26, n.length, true)
  const cd = new DataView(new ArrayBuffer(46))
  cd.setUint32(0, 0x02014b50, true)
  cd.setUint32(20, data.length, true)
  cd.setUint32(24, data.length, true)
  cd.setUint16(28, n.length, true)
  cd.setUint32(42, 0, true)
  const end = new DataView(new ArrayBuffer(22))
  end.setUint32(0, 0x06054b50, true)
  end.setUint16(8, 1, true)
  end.setUint16(10, 1, true)
  end.setUint32(12, 46 + n.length, true)
  end.setUint32(16, 30 + n.length + data.length, true)
  return new Blob([local.buffer, n as BlobPart, data as BlobPart, cd.buffer, n as BlobPart, end.buffer])
}
