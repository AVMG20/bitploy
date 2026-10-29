# Stage 1: build the site with Node.
FROM node:24-alpine AS build
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

# Stage 2: serve the built files with nginx. Vite, Vue and React write to "dist";
# Create React App writes to "build" (change the path in the last COPY).
FROM nginxinc/nginx-unprivileged:alpine
COPY <<'NGINXCONF' /etc/nginx/conf.d/default.conf
server {
    listen 8080;
    root /usr/share/nginx/html;
    index index.html;
    location / {
        # Single-page app: unknown paths load index.html so client-side routes work.
        try_files $uri $uri/ /index.html;
    }
}
NGINXCONF
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 8080
