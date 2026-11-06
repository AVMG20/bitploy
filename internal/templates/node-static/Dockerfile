# Stage 1: build the site with Node.
FROM node:22-bookworm-slim AS build
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

# Stage 2: serve the built files with nginx. Astro writes to "dist";
# Eleventy writes to "_site" (change the path in the last COPY).
FROM nginxinc/nginx-unprivileged:alpine
COPY <<'NGINXCONF' /etc/nginx/conf.d/default.conf
server {
    listen 8080;
    root /usr/share/nginx/html;
    index index.html;
    # Behind the proxy the port nginx listens on (8080) must not end up in redirects.
    absolute_redirect off;
    # Shows 404.html when the site has one.
    error_page 404 /404.html;
    # Hidden files such as .env or .git are never served; /.well-known/ is.
    location ~ /\.(?!well-known/) {
        deny all;
    }
    location / {
        # Every page is a real file: /about finds about.html or about/index.html. Unknown paths are a 404.
        try_files $uri $uri/ $uri.html =404;
    }
}
NGINXCONF
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 8080
