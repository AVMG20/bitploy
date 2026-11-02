import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { detectedType, detectSource, NEXT_CONFIG_NAMES, outputDirError } from '../src/lib/detect.ts'
import type { Detected } from '../src/lib/detect.ts'
import { listZipNames, readZipText, zipNameAtTop } from '../src/lib/zip.ts'
import type { BuildMode } from '../src/lib/types.ts'

const files = ['package.json', 'package-lock.json', 'src/main.js']
const vite = JSON.stringify({ scripts: { build: 'vite build' }, devDependencies: { vite: '^5' } })
const start = JSON.stringify({ scripts: { start: 'node server.js' } })
const neither = JSON.stringify({ scripts: { test: 'jest' } })

const detect = (paths: string[], pkg: string | null, mode: BuildMode = 'auto', nextConfig: string | null = null) =>
  detectSource(paths, mode, '', 'Dockerfile', pkg, nextConfig)

// The same table as the server's TestPlanBuildNodeDetection
// (internal/deploy/nodeproject_test.go): one file, so the two cannot drift apart.
interface Case {
  name: string
  mode: BuildMode
  files: string[]
  package?: { scripts: string[] | Record<string, string>; deps: string[]; main?: string }
  raw_package?: string
  next_config?: string
  go_only?: boolean
  expect: { type?: string; output?: string; spa?: boolean; unsure?: boolean; warn?: boolean; none?: boolean; dockerfile?: boolean }
}

function packageText(c: Case): string | null {
  if (c.raw_package !== undefined) return c.raw_package
  if (!c.package) return null
  const scripts = Array.isArray(c.package.scripts) ? Object.fromEntries(c.package.scripts.map((n) => [n, 'x'])) : c.package.scripts
  return JSON.stringify({ scripts, dependencies: Object.fromEntries(c.package.deps.map((d) => [d, '^1'])), main: c.package.main })
}

const cases: Case[] = JSON.parse(readFileSync(new URL('../../internal/deploy/testdata/detect_cases.json', import.meta.url), 'utf8'))

test('the shared table is there', () => {
  assert.ok(cases.length >= 50)
})

for (const c of cases.filter((x) => !x.go_only)) {
  test(`detection: ${c.name}`, () => {
    const hasConfig = NEXT_CONFIG_NAMES.some((n) => c.files.includes(n))
    // As the drop zone does: no package.json in the project is not the same as one it could not read.
    const d = detect(c.files, packageText(c), c.mode, hasConfig ? (c.next_config ?? '') : null)
    const want = c.expect
    if (want.none) {
      assert.equal(d.kind, 'none', JSON.stringify(d))
      return
    }
    if (want.dockerfile) {
      assert.equal(d.kind, 'dockerfile')
      return
    }
    assert.equal(d.kind, 'template', JSON.stringify(d))
    if (d.kind !== 'template') return
    assert.equal(d.type, want.type)
    assert.equal(!!d.unsure, !!want.unsure)
    assert.equal(!!d.warn, !!want.warn)
    if (want.type === 'static_build') {
      assert.equal(d.output, want.output)
      assert.equal(d.spa, want.spa)
    }
  })
}

test('Vite with a build script and index.html is a static build with an SPA fallback', () => {
  assert.deepEqual(detect([...files, 'index.html'], vite), {
    kind: 'template', type: 'static_build', label: 'Static build', known: true, framework: 'Vite', output: 'dist', spa: true,
  })
})

test('the type of a detected source has a name for the select', () => {
  const name = (d: Detected) => detectedType(d)
  assert.equal(name(detect([...files, 'index.html'], vite)), 'Static build')
  assert.equal(name(detect(files, start)), 'Node.js app')
  assert.equal(name(detect(['index.html'], null)), 'Plain website')
  assert.equal(name(detect(['Dockerfile', ...files], vite)), 'Own Dockerfile')
  assert.equal(name(detect(['README.md'], null)), '')
  assert.equal(detectedType(null), '')
  assert.equal(name(detect(files, null)), '')
})

test('a start script is a Node.js app, built first when there is a build script', () => {
  const d = detect(files, start)
  assert.equal(d.kind === 'template' && d.type, 'node')
  assert.equal(d.kind === 'template' && d.build, false)
  const both = JSON.stringify({ scripts: { build: 'tsc', start: 'node dist/server.js' } })
  assert.equal((detect(files, both) as { build?: boolean }).build, true)
})

test('the Next.js config is read for its output, comments aside', () => {
  const pkg = JSON.stringify({ scripts: { build: 'next build', start: 'next start' }, dependencies: { next: '16' } })
  const withConfig = [...files, 'next.config.mjs']
  assert.equal((detect(withConfig, pkg, 'auto', "export default { output: 'export' }") as { type?: string }).type, 'static_build')
  assert.equal((detect(withConfig, pkg, 'auto', "// output: 'export'\nexport default {}") as { type?: string }).type, 'node')
  assert.equal((detect(withConfig, pkg, 'auto', null) as { type?: string }).type, 'node')
})

test('an unreadable package.json gives a neutral preview in every mode', () => {
  assert.deepEqual(detect(files, null), { kind: 'template', type: 'unknown', label: 'Node.js project', known: false })
  const sb = detect(files, null, 'static_build')
  assert.equal(sb.kind === 'template' && sb.known, false)
  assert.equal(detect(['index.html'], null, 'node').kind, 'none')
})

test('vite without index.html at the top is not a site', () => {
  assert.equal(detect(files, vite).kind, 'none')
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

test('output folder: the same rule as the server', () => {
  for (const ok of ['', '  ', 'dist', './build/', 'build/client', 'docs/.vitepress/dist', '_site', 'web\\out']) assert.equal(outputDirError(ok), '', ok)
  for (const bad of ['/etc', '../x', 'a/../b', 'a b', 'a;b', '.', './', 'a//b', 'dist*', 'x'.repeat(201)]) assert.notEqual(outputDirError(bad), '', bad)
})
