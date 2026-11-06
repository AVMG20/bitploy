import assert from 'node:assert/strict'
import { test } from 'node:test'
import { BUILD_CHOICES, buildChoice, buildChoiceOf, buildTag, looksBuildTime } from '../src/lib/buildVars.ts'

test('names a front end reads while it builds', () => {
  for (const n of ['VITE_SUPABASE_URL', 'NEXT_PUBLIC_API_URL', 'PUBLIC_SITE', 'react_app_key', ' NUXT_PUBLIC_X ']) {
    assert.equal(looksBuildTime(n), true, n)
  }
  for (const n of ['VITE_', 'DATABASE_URL', 'API_KEY', 'NEXT_PUBLICITY', '']) {
    assert.equal(looksBuildTime(n), false, n)
  }
})

test('a variable without build is only for the running app', () => {
  assert.equal(buildChoiceOf({}), 'none')
  assert.equal(buildChoiceOf({ build: 'secret' }), 'secret')
  assert.equal(buildTag({}), '')
  assert.equal(buildTag({ build: 'public' }), 'build, public')
  assert.equal(buildChoice('secret').value, 'secret')
  assert.deepEqual(
    BUILD_CHOICES.map((c) => c.value),
    ['none', 'public', 'secret'],
  )
})
