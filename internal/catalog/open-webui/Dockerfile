# The slim image leaves out the bundled speech and embedding models (the full
# image is over bitploy's size limit); they download on first use into /data/cache.
FROM ghcr.io/open-webui/open-webui:v0.13.0-slim
ENV DATA_DIR=/data \
    HOME=/tmp \
    WHISPER_MODEL_DIR=/data/cache/whisper/models \
    SENTENCE_TRANSFORMERS_HOME=/data/cache/embedding/models \
    HF_HOME=/data/cache/embedding/models \
    TIKTOKEN_CACHE_DIR=/data/cache/tiktoken \
    ENABLE_OLLAMA_API=false \
    HOST=0.0.0.0
# The image runs as root and copies its frontend assets into the static folder
# on every start, so that folder must belong to the app user.
RUN useradd --system --uid 10001 --home-dir /tmp --no-create-home app && \
    chown -R app:app /app/backend/open_webui/static
USER app
EXPOSE 8080
# Startup loads a large Python app and runs migrations; on half a CPU that can take minutes.
HEALTHCHECK --interval=30s --timeout=5s --start-period=3m --start-interval=2s --retries=3 \
  CMD ["curl", "-fs", "-o", "/dev/null", "http://127.0.0.1:8080/health"]
