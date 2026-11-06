import assert from 'node:assert/strict'
import { test } from 'node:test'
import { looksSecretName } from '../src/lib/secretName.ts'

// The same cases as TestLooksSecretName in internal/mcp/env_hidden_test.go.
test('a name that holds a secret', () => {
  const cases: Record<string, boolean> = {
    STRIPE_SECRET_KEY: true,
    github_token: true,
    API_KEY: true,
    DB_PASSWORD: true,
    Monkey: true,
    NODE_ENV: false,
    DATABASE_URL: false,
    VITE_API_URL: false,
    PORT_NUMBER: false,
  }
  for (const [name, want] of Object.entries(cases)) assert.equal(looksSecretName(name), want, name)
})
