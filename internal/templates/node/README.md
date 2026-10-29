# node

For a Node.js server (Express, Fastify, Hono, Next.js in server mode, a bot with a web port).

- `package.json` and `package-lock.json` must exist (the build runs `npm ci`). No lock file: change `npm ci` to `npm install`.
- Needs a `start` script in `package.json`, or change `CMD` to `["node", "server.js"]`.
- Listen on `0.0.0.0` and `process.env.PORT`, for example `app.listen(process.env.PORT || 3000, "0.0.0.0")`.
  Listening on `localhost` or `127.0.0.1` makes the deploy fail with `unhealthy`.
- If the app needs a build step (TypeScript, Next.js), add `RUN npm run build` after `COPY . .` and
  install dev dependencies first: replace `npm ci --omit=dev` with `npm ci` and set `NODE_ENV=production` after the build.
- If `EXPOSE` is not your port, change both, or set the port with update_app.
