FROM searxng/searxng:2026.11.9-57ff753ad
# SearXNG keeps nothing worth saving: its settings are written here and the
# secret key comes from SEARXNG_SECRET. The settings add the JSON format, so
# tools such as Open WebUI can search through it; without this file the image
# makes its own with the HTML format only.
COPY --chown=977:977 <<EOF /etc/searxng/settings.yml
use_default_settings: true
search:
  formats:
    - html
    - json
server:
  image_proxy: true
EOF
ENV GRANIAN_HOST=0.0.0.0 \
    GRANIAN_PORT=8080
USER 977:977
EXPOSE 8080
