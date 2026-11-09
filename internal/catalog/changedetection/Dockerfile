FROM dgtlmoon/changedetection.io:0.70.2
# changedetection.io keeps its watches and snapshots in the datastore folder
# (the image's default is /datastore). Requests come through Cloudflare and
# Traefik (subdomains) or Traefik alone (custom domains); with two trusted
# proxies the real client is found on both paths.
ENV USE_X_SETTINGS=1 \
    USE_X_SETTINGS_PROXY_COUNT=2
USER 10001:10001
EXPOSE 5000
CMD ["python", "./changedetection.py", "-d", "/data"]
