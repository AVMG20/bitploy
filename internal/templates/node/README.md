# node

For a Node.js server (Express, Fastify, Hono, Next.js in server mode) or a bot.

- `package.json` and `package-lock.json` must exist (the build runs `npm ci`). No lock file: change `npm ci` to `npm install`.
- Needs a `start` script in `package.json`, or change `CMD` to `["node", "server.js"]`.
- Listen on `0.0.0.0` and `process.env.PORT`, for example `app.listen(process.env.PORT || 3000, "0.0.0.0")`.
  Listening on `localhost` or `127.0.0.1` makes the deploy fail with `unhealthy`.
- Bot or queue worker without a web port (Discord, Telegram): create the app with `type=worker`. `EXPOSE` is then ignored. The process must stay running (for example `client.login(process.env.DISCORD_TOKEN)`). Store the token with `set_env`, never in a file.
- If the app needs a build step (TypeScript, Next.js), add `RUN npm run build` after `COPY . .` and
  install dev dependencies first: replace `npm ci --omit=dev` with `npm ci` and set `NODE_ENV=production` after the build.
- Keeping files (SQLite, uploads): turn on the volume (`update_app` volume=true, then `restart_app`), write only to `/data`, and add `RUN mkdir /data && chown node:node /data` before `USER node`.
- If `EXPOSE` is not your port, change both, or set the port with update_app.
