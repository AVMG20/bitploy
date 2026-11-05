# The ultra-lite image leaves out OCR, LibreOffice and the login module (the
# full image is over bitploy's size limit).
FROM stirlingtools/stirling-pdf:3.2.2-ultra-lite
# Settings, custom files and pipelines go under STIRLING_BASE_PATH. The image
# starts as root and drops to stirlingpdfuser (uid 1000) itself; starting as
# 1000 only needs the log folder, the one folder the image leaves to root.
ENV STIRLING_BASE_PATH=/data/
USER root
RUN chown -R 1000:1000 /logs
USER 1000:1000
EXPOSE 8080
# Java and Spring start slowly on half a CPU.
HEALTHCHECK --interval=30s --timeout=5s --start-period=3m --start-interval=2s --retries=3 \
  CMD ["curl", "-fs", "-o", "/dev/null", "http://127.0.0.1:8080/api/v1/info/status"]
