# Stage 1: install dependencies and build. next.config needs: output: "standalone"
FROM node:24-alpine AS build
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
# Next.js copies "public" below; make sure it exists.
RUN mkdir -p public
RUN npm run build

# Stage 2: run only the standalone output (no node_modules install, small image).
FROM node:24-alpine
ENV NODE_ENV=production \
    HOSTNAME=0.0.0.0 \
    PORT=3000
WORKDIR /app
COPY --from=build --chown=node:node /app/public ./public
COPY --from=build --chown=node:node /app/.next/standalone ./
COPY --from=build --chown=node:node /app/.next/static ./.next/static

# Run as the unprivileged "node" user that ships with the image.
USER node

# The server listens on 0.0.0.0 (HOSTNAME) and PORT (default 3000).
EXPOSE 3000

CMD ["node", "server.js"]
