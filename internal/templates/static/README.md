# static

For a website made of plain files: `index.html`, CSS, JavaScript, images.
Nothing runs on the server except nginx, which serves your files.

- Put `index.html` at the top of the folder next to this Dockerfile.
- The app listens on port 8080 (already set by `EXPOSE 8080`). Nothing to change.
- Missing pages return 404. For a single-page app built with a bundler use the `node-spa` template instead.
