# node

For a Node.js server (Express, Fastify, Hono) or a bot. Next.js: use the `nextjs` template.

- `package.json` and `package-lock.json` must exist (the build runs `npm ci`). No lock file: change `npm ci` to `npm install`. For pnpm, yarn or bun the adapted template changes the install lines.
- Ship the lock file. Without one every build installs the newest versions `package.json` allows, and a build can break although the code did not change; pin exact versions (`"3.13.0"`, not `"^3.13.0"`) if there is no lock file.
- The image is Debian slim (`node:22-bookworm-slim`) so native modules (better-sqlite3, sqlite3, bcrypt, argon2, sharp, canvas) install ready-made binaries. Do not switch to an `-alpine` image: there they are compiled from source, which needs python3, make and g++ and can take many minutes or fail. If a module still compiles and fails, add `RUN apt-get update && apt-get install -y --no-install-recommends python3 make g++ && rm -rf /var/lib/apt/lists/*` before the install.
- Needs a `start` script in `package.json`, or change `CMD` to `["node", "server.js"]`.
- Listen on `0.0.0.0` and `process.env.PORT`, for example `app.listen(process.env.PORT || 3000, "0.0.0.0")`.
  Listening on `localhost` or `127.0.0.1` makes the deploy fail with `unhealthy`.
- Bot or queue worker without a web port (Discord, Telegram): create the app with `type=worker`. `EXPOSE` is then ignored. The process must stay running (for example `client.login(process.env.DISCORD_TOKEN)`). Store the token with `set_env`, never in a file.
- If the app needs a build step (TypeScript, NestJS, SvelteKit, Remix), use two stages so the build tools stay out of the image. Next.js: use the `nextjs` template. Nuxt: the second stage copies only `.output` (`COPY --from=build /app/.output ./.output`, `CMD ["node", ".output/server/index.mjs"]`) and needs no node_modules. Otherwise:

  ```dockerfile
  FROM node:22-bookworm-slim AS build
  WORKDIR /app
  COPY package*.json ./
  RUN npm ci
  COPY . .
  RUN npm run build
  # Drop the dev dependencies (TypeScript, bundlers): the app does not run them.
  RUN npm prune --omit=dev

  FROM node:22-bookworm-slim
  ENV NODE_ENV=production
  WORKDIR /app
  COPY --from=build /app ./
  USER node
  EXPOSE 3000
  CMD ["npm", "start"]
  ```

  A package the app needs while it runs (also `tsx` or `ts-node` in the start command) must be in `dependencies`, not `devDependencies`. Never use a full `npm ci` in a single-stage Dockerfile: the dev dependencies often make up most of the image.
- Keeping files (SQLite, uploads): turn on the volume, write only to `/data`, and add `RUN mkdir /data && chown node:node /data` before `USER node`.
- If `EXPOSE` is not your port, change both, or set the port with update_app.
