# IT-Tools is a static site. Its own image runs nginx as root on port 80,
# so the files are copied onto the unprivileged nginx image.
FROM corentinth/it-tools:2024.10.22-7ca5933 AS site

FROM nginxinc/nginx-unprivileged:1.28.2-alpine
COPY --from=site /usr/share/nginx/html /usr/share/nginx/html
RUN printf 'server {\n  listen 8080;\n  root /usr/share/nginx/html;\n  index index.html;\n  location / {\n    try_files $uri $uri/ /index.html;\n  }\n}\n' > /etc/nginx/conf.d/default.conf
USER 101
EXPOSE 8080
