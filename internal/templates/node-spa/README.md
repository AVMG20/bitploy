# node-spa

For a single-page app that is built to static files: Vue, React, Svelte or Angular with Vite and similar tools.

- `package.json` must have a `build` script, and `package-lock.json` must exist (the build runs `npm ci`; without a lock file change it to `npm install`).
- The build output folder must be `dist` (Vite default). For Create React App it is `build`: change the path in the last `COPY`.
- Unknown URLs fall back to `index.html`, so client-side routing works on reload.
- Build-time settings (`VITE_*`) are baked in during the build. Set them with `set_env` and `build={"VITE_API_URL":"public"}`; they reach the build on the next deploy. Sent without a Dockerfile (the usual way for a site), bitploy hands them to `npm run build` itself. With this Dockerfile of your own, add `ARG VITE_API_URL` on the line before `RUN npm run build`. Never put secrets in them: everything in the built files is public.
- The result is only static files. An API the site calls must be its own app (use the `node` or `python` template) and the site must call that app's full address.
- For an app with a Node server instead, use the `node` template.
- Do not send `node_modules` or `dist`; the build creates them.
