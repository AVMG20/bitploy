// Dev only: the dashboard against an in-memory API (dev/mock-api.ts) instead of
// the Go server. Run: npx vite --config vite.mock.config.ts --port 5174
// Never used by `npm run build`.
import { defineConfig } from 'vite'
import base from './vite.config'
import { mockApi } from './dev/mock-api'

export default defineConfig({
  ...base,
  plugins: [...(base.plugins ?? []), mockApi()],
  // No proxy: /api/v1 is answered by the mock.
  server: { ...base.server, port: 5174, proxy: undefined },
})
