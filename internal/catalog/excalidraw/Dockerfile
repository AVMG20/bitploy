# Excalidraw publishes no version tags, only "latest": this pins the image
# built on 2026.05.06 by digest. It is a static site; its own image runs nginx
# as root on port 80, so the files are copied onto the unprivileged nginx image.
FROM excalidraw/excalidraw@sha256:f7ee194addd607bf831d2af0f0a34463dd4225e426cf35199ef0b12a803398e9 AS site

FROM nginxinc/nginx-unprivileged:1.30.6-alpine
COPY --from=site /usr/share/nginx/html /usr/share/nginx/html
# worker_processes auto starts one worker per host CPU; two are plenty here.
RUN sed -i 's/^worker_processes .*/worker_processes 2;/' /etc/nginx/nginx.conf && \
    printf 'server {\n  listen 8080;\n  root /usr/share/nginx/html;\n  index index.html;\n  location / {\n    try_files $uri $uri/ /index.html;\n  }\n}\n' > /etc/nginx/conf.d/default.conf
USER 101
EXPOSE 8080
