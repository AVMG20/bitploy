# nextjs

For a Next.js app that runs its own server (App Router, API routes, server components).
For a Next.js site exported to plain files (`output: "export"`) use `node-static` instead.

- Add `output: "standalone"` to `next.config.js` or `next.config.ts`. Without it the build has no `.next/standalone` folder and the image fails.
- `package-lock.json` must exist (the build runs `npm ci`; without a lock file the adapted template uses `npm install`).
- The server listens on `0.0.0.0` and `PORT` (3000) by itself. `node server.js` is the start command; `next start` is not used.
- Environment variables used while the server runs: `set_env`, then `restart_app`. `NEXT_PUBLIC_*` values are baked in during the build and are public; add them as `ENV` lines before `RUN npm run build`, never put secrets there.
- Keeping files (SQLite, uploads): turn on the volume (`create_app` with volume=true for a new app; an existing app: `update_app` volume=true, then `restart_app`), write only to `/data`, and add `RUN mkdir /data && chown node:node /data` before `USER node`. A database means SQLite in `/data` (one container per app, no Postgres or MySQL server).
- If `EXPOSE` is not your port, change both, or set the port with update_app.
