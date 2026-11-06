import assert from 'node:assert/strict'
import { test } from 'node:test'
import { highlightEntry, highlightLog } from '../src/lib/logHighlight.ts'

const plain = (line: string) => highlightLog(line).tokens.map((t) => t.text).join('')
const kinds = (line: string) => highlightLog(line).tokens.filter((t) => t.type !== 'text').map((t) => `${t.type}:${t.text}`)
const level = (line: string) => highlightLog(line).level

test('the tokens add up to the line, whatever it is', () => {
  const samples = [
    '',
    '   ',
    '<script>alert(1)</script>',
    '2026-11-06T10:00:00.123Z ERROR boom at http://x.test/a?b=1',
    '#5 [build 2/6] RUN npm ci',
    '#5 0.512 npm warn deprecated',
    '10.0.0.1 - - [06/Nov/2026:10:00:00 +0000] "GET /api HTTP/1.1" 404 512 "-" "curl/8.5"',
    '{"level":"error","msg":"x"}',
    '    at handler (/app/server.js:42:13)',
    'a.'.repeat(3000),
    'x'.repeat(5000),
  ]
  for (const s of samples) assert.equal(plain(s), s, s.slice(0, 60))
})

test('levels: upper-case words, key=value, JSON, brackets, a leading "error:", npm', () => {
  assert.equal(level('2026-11-06 10:00:00 ERROR could not connect'), 'error')
  assert.equal(level('[2026-11-06 10:00:00] production.WARNING: slow'), 'warn')
  assert.equal(level('INFO  server started'), 'info')
  assert.equal(level('DEBUG pool size=10'), 'debug')
  assert.equal(level('time=x level=warn msg="slow"'), 'warn')
  assert.equal(level('{"level":"error","msg":"boom"}'), 'error')
  assert.equal(level('{"severity":"DEBUG"}'), 'debug')
  assert.equal(level('2026/11/06 10:00:00 [error] 29#29: open() failed'), 'error')
  assert.equal(level('[notice] start worker processes'), 'info')
  assert.equal(level('error: failed to push some refs'), 'error')
  assert.equal(level('npm ERR! code ELIFECYCLE'), 'error')
  assert.equal(level('npm warn deprecated inflight@1.0.6'), 'warn')
  assert.equal(level('panic: runtime error: index out of range'), 'error')
  assert.equal(level('Traceback (most recent call last):'), 'error')
  assert.equal(level("TypeError: Cannot read properties of undefined (reading 'id')"), 'error')
  assert.equal(level('java.lang.IllegalStateException: closed'), 'error')
})

test('ordinary words are not levels', () => {
  assert.equal(level('error handling middleware loaded'), null)
  assert.equal(level('0 errors, 2 warnings'), null)
  assert.equal(level('Information about the [project]'), null)
  assert.equal(level('ErrorBoundary mounted'), null)
  assert.deepEqual(kinds('[project] ready'), [])
})

test('the level value is coloured, its key is a key', () => {
  assert.deepEqual(kinds('level=warn msg="slow"'), ['key:level=', 'warn:warn', 'key:msg'])
  assert.deepEqual(kinds('{"level":"error"}'), ['key:level":"', 'error:error'])
})

test('Docker build steps: BuildKit, the classic builder, bitploy\'s own steps', () => {
  assert.deepEqual(kinds('#5 [build 2/6] RUN npm ci'), ['stepnum:#5', 'step:[build 2/6]', 'keyword:RUN'])
  assert.deepEqual(kinds('#1 [internal] load build definition from Dockerfile'), ['stepnum:#1', 'step:[internal]'])
  assert.deepEqual(kinds('#5 DONE 12.3s'), ['stepnum:#5', 'done:DONE', 'number:12.3s'])
  assert.deepEqual(kinds('#7 CACHED'), ['stepnum:#7', 'done:CACHED'])
  assert.deepEqual(kinds('#8 12.34 npm warn deprecated'), ['stepnum:#8', 'time:12.34', 'warn:warn'])
  assert.deepEqual(kinds('#9 ERROR: process "/bin/sh -c npm run build" did not complete'), ['stepnum:#9', 'error:ERROR'])
  assert.deepEqual(kinds('Step 3/7 : COPY . .'), ['step:Step 3/7 :', 'keyword:COPY'])
  assert.deepEqual(kinds('[1/3] Fetching the source'), ['step:[1/3]'])
  // The step's command is not key=value logging.
  assert.deepEqual(kinds('#7 [build 4/6] RUN --mount=type=cache npm ci'), ['stepnum:#7', 'step:[build 4/6]', 'keyword:RUN'])
  // Not an instruction after the step: no keyword.
  assert.deepEqual(kinds('#2 [auth] library/node:pull token'), ['stepnum:#2', 'step:[auth]'])
})

test('HTTP: method, path, the status after them, durations', () => {
  assert.deepEqual(kinds('GET /products 200 11ms'), ['method:GET', 'path:/products', 'status2:200', 'number:11ms'])
  assert.deepEqual(kinds('POST /cart 302 2ms'), ['method:POST', 'path:/cart', 'status3:302', 'number:2ms'])
  assert.deepEqual(kinds('10.0.0.1 - - [06/Nov/2026:10:00:00 +0000] "GET /api?page=2 HTTP/1.1" 404 512 "-"'), [
    'time:06/Nov/2026:10:00:00 +0000',
    'method:GET',
    'path:/api?page=2',
    'status4:404',
  ])
  assert.deepEqual(kinds('request done status=503'), ['key:status', 'status5:503'])
  // A number without a method or a status key is just a number.
  assert.deepEqual(kinds('added 412 packages in 9s'), ['number:9s'])
  assert.deepEqual(kinds('version 1.200.3 ready'), [])
})

test('timestamps and URLs', () => {
  assert.deepEqual(kinds('2026-11-06T10:00:00.123Z INFO Listening on http://0.0.0.0:3000.'), [
    'time:2026-11-06T10:00:00.123Z',
    'info:INFO',
    'url:http://0.0.0.0:3000',
  ])
  assert.deepEqual(kinds('2026-11-06 10:00:00.123 UTC [1] LOG:  ready'), ['time:2026-11-06 10:00:00.123 UTC', 'info:LOG'])
  assert.deepEqual(kinds('see (https://example.com/docs?a=1#x), then'), ['url:https://example.com/docs?a=1#x'])
})

test('stack trace frames step back, their file and line stand out', () => {
  const js = highlightLog('    at handler (/app/server.js:42:13)')
  assert.equal(js.trace, true)
  assert.deepEqual(kinds('    at handler (/app/server.js:42:13)'), ['location:/app/server.js:42:13'])
  assert.deepEqual(kinds('    at node:internal/main:1228:14'), ['location:node:internal/main:1228:14'])
  assert.deepEqual(kinds('  File "/app/main.py", line 12, in <module>'), ['location:"/app/main.py", line 12'])
  assert.equal(highlightLog('\t/app/main.go:42 +0x1d').trace, true)
  assert.equal(highlightLog('goroutine 1 [running]:').trace, true)
  assert.equal(highlightLog('#0 /var/www/app.php(12): run()').trace, true)
  assert.deepEqual(kinds('Caused by: java.io.IOException: boom'), ['error:java.io.IOException'])
  // A frame printed by a build step keeps its step number.
  assert.equal(highlightLog('#9 3.951     at error (file:///app/x.js:391:42)').trace, true)
  assert.deepEqual(kinds('#9 3.951     at error (file:///app/x.js:391:42)'), ['stepnum:#9', 'time:3.951', 'location:file:///app/x.js:391:42'])
  // A build step is not a PHP frame.
  assert.equal(highlightLog('#3 [2/4] RUN npm ci').trace, false)
})

test('a very long line keeps its level but is not coloured', () => {
  const long = 'ERROR ' + 'x'.repeat(10_000)
  const info = highlightLog(long)
  assert.equal(info.level, 'error')
  assert.deepEqual(info.tokens, [{ type: 'text', text: long }])
})

test('an entry is worked out once', () => {
  const e = { text: 'GET / 200 1ms' }
  assert.equal(highlightEntry(e), highlightEntry(e))
})

test('thousands of lines stay cheap', () => {
  const lines = Array.from({ length: 5000 }, (_, i) =>
    i % 3 ? `2026-11-06T10:00:${String(i % 60).padStart(2, '0')}Z INFO GET /items/${i} 200 ${i % 90}ms` : `#${i} [build 2/6] RUN npm ci --omit=dev && echo done ${i}`,
  )
  const start = performance.now()
  for (const l of lines) highlightLog(l)
  // Generous: on a laptop this is a few milliseconds.
  assert.ok(performance.now() - start < 1000)
})
