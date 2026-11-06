# nextjs

For a Next.js app that runs its own server (App Router, API routes, server components).
For a Next.js site exported to plain files (`output: "export"`) use `node-static` instead.

- Add `output: "standalone"` to `next.config.js` or `next.config.ts`. Without it the build has no `.next/standalone` folder and the image fails.
- `package-lock.json` must exist (the build runs `npm ci`; without a lock file the adapted template uses `npm install`, and every build installs the newest versions `package.json` allows, so ship the lock file).
- The image is Debian slim (`node:22-bookworm-slim`) so native modules (better-sqlite3, sharp) install ready-made binaries. Do not switch to an `-alpine` image.
- The server listens on `0.0.0.0` and `PORT` (3000) by itself. `node server.js` is the start command; `next start` is not used.
- Environment variables used while the server runs: `set_env`, then `restart_app`. `NEXT_PUBLIC_*` values are baked in during the build and are public: set them with `set_env` and `build={"NEXT_PUBLIC_API_URL":"public"}` and add `ARG NEXT_PUBLIC_API_URL` on the line before `RUN npm run build`; they reach the build on the next deploy. Never put secrets there. A key only the build needs (like `SENTRY_AUTH_TOKEN`): `build={"SENTRY_AUTH_TOKEN":"secret"}` and `RUN --mount=type=secret,id=SENTRY_AUTH_TOKEN,env=SENTRY_AUTH_TOKEN npm run build`; it never ends up in the image.
- Keeping files (SQLite, uploads): turn on the volume, write only to `/data`, and add `RUN mkdir /data && chown node:node /data` before `USER node`.
- If `EXPOSE` is not your port, change both, or set the port with update_app.
