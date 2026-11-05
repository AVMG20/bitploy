# Docmost keeps uploads in /app/data/storage, a folder its image declares as a
# VOLUME: Docker would mount a throwaway volume there (or, through a link, over
# /data) and attachments would be gone after the next deploy. A Dockerfile
# cannot drop a VOLUME, so the app is copied onto the Node.js image Docmost is
# built on (its image says NODE_VERSION=26.12.1) and the folder points at /data.
FROM docmost/docmost:0.97.0 AS docmost

FROM node:26.12.1-slim
COPY --from=docmost --chown=node:node /app /app
RUN rm -rf /app/data/storage && ln -s /data /app/data/storage
# The image starts with "pnpm start", which runs this from apps/server.
ENV NODE_ENV=production
WORKDIR /app/apps/server
USER node
EXPOSE 3000
# The first start creates the database tables, which can take a while on half a CPU.
HEALTHCHECK --interval=30s --timeout=5s --start-period=3m --start-interval=2s --retries=3 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:3000/api/health').then(r => process.exit(r.ok ? 0 : 1), () => process.exit(1))"]
CMD ["node", "dist/main"]
