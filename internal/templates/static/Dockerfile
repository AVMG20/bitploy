FROM nginxinc/nginx-unprivileged:alpine
COPY <<'NGINXCONF' /etc/nginx/conf.d/default.conf
server {
    listen 8080;
    root /usr/share/nginx/html;
    index index.html index.htm;
    # Behind the proxy the port nginx listens on (8080) must not end up in redirects.
    absolute_redirect off;
    # Shows 404.html when the site has one.
    error_page 404 /404.html;
    # Hidden files such as .env or .git are never served; /.well-known/ is.
    location ~ /\.(?!well-known/) {
        deny all;
    }
    location / {
        # /about finds about.html, /docs finds docs/index.html. Unknown paths are a 404.
        try_files $uri $uri/ $uri.html =404;
    }
}
NGINXCONF
COPY . /usr/share/nginx/html
EXPOSE 8080
