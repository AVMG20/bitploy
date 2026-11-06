import assert from 'node:assert/strict'
import { test } from 'node:test'
import { deployErrorSummary, firstErrorLine, firstRuntimeError, hookErrorLine } from '../src/lib/buildError.ts'

const VITE_FAIL = [
  '#7 [build 5/6] RUN apt-get install -y curl',
  '#7 1.200 debconf: unable to initialize frontend: Dialog',
  '#7 DONE 2.1s',
  '#9 [build 6/6] RUN npm run build',
  '#9 0.499 > vite build',
  '#9 3.950 error during build:',
  '#9 3.950 src/pages/Checkout.tsx (14:9): "formatPrice" is not exported by "src/lib/money.ts", imported by "src/pages/Checkout.tsx".',
  '#9 3.951     at getRollupError (file:///app/node_modules/rollup/dist/es/shared/parseAst.js:395:41)',
  '#9 ERROR: process "/bin/sh -c npm run build" did not complete successfully: exit code: 1',
  '------',
  ' > [build 6/6] RUN npm run build:',
  '------',
  'Dockerfile:11',
  'ERROR: failed to solve: process "/bin/sh -c npm run build" did not complete successfully: exit code: 1',
]
const FORMAT_PRICE = 'src/pages/Checkout.tsx (14:9): "formatPrice" is not exported by "src/lib/money.ts", imported by "src/pages/Checkout.tsx".'

test('the real error of a build, not the docker wrapper or an earlier step', () => {
  assert.equal(firstErrorLine(VITE_FAIL), FORMAT_PRICE)
})

test('a typescript error, and the last output of a step that never says error', () => {
  assert.equal(
    firstErrorLine([
      '#8 [4/5] RUN npx tsc',
      "#8 2.1 src/app.ts(3,5): error TS2304: Cannot find name 'foo'.",
      '#8 2.2 npm error code 2',
      '#8 ERROR: process "/bin/sh -c npx tsc" did not complete successfully: exit code: 2',
    ]),
    "src/app.ts(3,5): error TS2304: Cannot find name 'foo'.",
  )
  assert.equal(
    firstErrorLine([
      '#6 [3/4] RUN go build -o /app .',
      '#6 0.9 # example.com/app',
      '#6 0.9 ./main.go:5:2: x declared and not used',
      '#6 ERROR: process "/bin/sh -c go build -o /app ." did not complete successfully: exit code: 1',
    ]),
    './main.go:5:2: x declared and not used',
  )
  assert.equal(firstErrorLine([]), '')
  assert.equal(firstErrorLine(undefined), '')
})

test('the summary puts the line of the server first, or finds it in the log', () => {
  assert.deepEqual(deployErrorSummary({ code: 'build_failed', message: `The build failed: ${FORMAT_PRICE}` }, []), {
    message: 'The build failed.',
    line: FORMAT_PRICE,
  })
  // An older deployment: only Docker's wrapper, or no line at all.
  const wrapper = 'The build failed: ERROR: failed to solve: process "/bin/sh -c npm run build" did not complete successfully: exit code: 1'
  assert.equal(deployErrorSummary({ code: 'build_failed', message: wrapper }, VITE_FAIL).line, FORMAT_PRICE)
  assert.equal(deployErrorSummary({ code: 'build_failed', message: 'The build failed.' }, VITE_FAIL).line, FORMAT_PRICE)
  assert.equal(deployErrorSummary({ code: 'build_failed', message: 'The build failed.' }, []).line, '')
  // The service of an app with several stays in the message.
  assert.equal(deployErrorSummary({ code: 'build_failed', message: `Service web: The build failed: ${FORMAT_PRICE}` }).message, 'Service web: The build failed.')
  // Other failures keep their message.
  assert.deepEqual(deployErrorSummary({ code: 'build_failed', message: 'The build ran out of memory.' }), { message: 'The build ran out of memory.', line: '' })
  assert.deepEqual(deployErrorSummary({ code: 'unhealthy', message: 'Your app did not answer.' }, ['ERROR x']), { message: 'Your app did not answer.', line: '' })
})

test('a crashed app shows the exception it printed', () => {
  const log = [
    '> node dist/server.js',
    '2026-11-06T10:00:05.288Z ERROR Unhandled error in POST /checkout',
    "TypeError: Cannot read properties of undefined (reading 'total')",
    '    at checkout (/app/dist/routes/checkout.js:42:31)',
  ]
  assert.equal(firstRuntimeError(log), "TypeError: Cannot read properties of undefined (reading 'total')")
  assert.equal(firstRuntimeError(['2026-11-06T10:00:05Z ERROR missing DATABASE_URL']), 'missing DATABASE_URL')
  assert.equal(deployErrorSummary({ code: 'app_crashed', message: 'Your app stopped right after starting (exited 1).' }, log).line, firstRuntimeError(log))
  assert.equal(firstRuntimeError(['listening on 8080']), '')
})

test('a failed before-deploy command shows the line of its own output that says why', () => {
  const tail = [
    '#14 DONE 2.1s',
    'Running before-deploy command: npx prisma migrate deploy',
    '[before-deploy] Datasource "db": PostgreSQL database "shop" at "postgres:5432"',
    '[before-deploy] Error: P3009 migrate found failed migrations in the target database',
    '[before-deploy] npm notice',
  ]
  assert.equal(
    deployErrorSummary({ code: 'before_deploy_failed', message: 'The before-deploy command failed with exit code 1.' }, tail).line,
    'Error: P3009 migrate found failed migrations in the target database',
  )
  assert.equal(hookErrorLine(['[after-deploy] cache cleared', '[after-deploy] done'], 'after-deploy'), 'done')
  assert.equal(hookErrorLine(['nothing of its own'], 'before-deploy'), '')
})
