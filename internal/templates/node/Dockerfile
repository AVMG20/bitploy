FROM node:22-bookworm-slim
# Debian slim, not Alpine: native modules (better-sqlite3, bcrypt, sharp) install
# ready-made binaries here instead of compiling from source.
ENV NODE_ENV=production
WORKDIR /app

# Install dependencies first so Docker can cache this layer.
COPY package*.json ./
RUN npm ci --omit=dev

COPY . .

# Run as the unprivileged "node" user that ships with the image.
USER node

# Your server must listen on 0.0.0.0 and process.env.PORT (default 3000),
# never on localhost or 127.0.0.1.
EXPOSE 3000

# Change this if your start command is not "npm start" (for example ["node", "server.js"]).
CMD ["npm", "start"]
