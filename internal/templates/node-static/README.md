# node-static

For a website with many pages that is built to static files: Astro, Eleventy, VitePress, Docusaurus.
For a single-page app (Vue, React, Svelte with client-side routing) use `node-spa` instead.

- `package.json` must have a `build` script, and `package-lock.json` must exist (the build runs `npm ci`; without a lock file the adapted template uses `npm install`).
- The build output folder must be `dist` (Astro and Vite default). Eleventy writes `_site`, Docusaurus `build`, VitePress `docs/.vitepress/dist`: change the path in the last `COPY`.
- No fallback to `index.html`. `/about` serves `about.html` or `about/index.html`, anything else is a 404 (your `404.html` is shown if the site has one).
- Astro must be in static mode (the default). If it uses an SSR adapter, use the `node` template.
- Build-time settings (`PUBLIC_*` in Astro, `VITE_*`) are baked in during the build. Set them with `set_env`; a change applies on the next deploy. Sent without a Dockerfile, bitploy hands every variable to the build script itself; with this Dockerfile, add `ARG PUBLIC_API_URL` on the line before `RUN npm run build`. Never put secrets in them: everything in the built files is public.
- An API the site calls must be its own app (use the `node` or `python` template) and the site must call that app's full address.
- Do not send `node_modules` or `dist`; the build creates them.
