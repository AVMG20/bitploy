# node-spa

For a single-page app that is built to static files: Vue, React, Svelte or Angular with Vite and similar tools.

- `package.json` must have a `build` script.
- The build output folder must be `dist` (Vite default). For Create React App it is `build`: change the path in the last `COPY`.
- Unknown URLs fall back to `index.html`, so client-side routing works on reload.
- Build-time settings (`VITE_*`) are baked in during the build. Never put secrets in them: everything in the built files is public.
- For an app with a Node server instead, use the `node` template.
