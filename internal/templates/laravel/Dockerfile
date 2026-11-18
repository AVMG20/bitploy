# Stage 1: PHP packages (commit composer.lock so versions stay the same).
FROM composer:2 AS vendor
WORKDIR /app
COPY composer.json composer.lock* ./
RUN composer install --no-dev --no-interaction --prefer-dist --no-scripts --no-autoloader --ignore-platform-reqs
COPY . .
RUN composer dump-autoload --no-dev --no-scripts --optimize

# Stage 2: Vite assets. No package.json? Delete this stage and the "COPY --from=assets" line below.
FROM node:22-bookworm-slim AS assets
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY --from=vendor /app ./
RUN npm run build

# Stage 3: Apache with PHP.
FROM php:8.4-apache
ENV APP_ENV=production \
    APP_DEBUG=false \
    LOG_CHANNEL=stderr \
    DB_CONNECTION=sqlite \
    DB_DATABASE=/data/database.sqlite \
    CACHE_STORE=file \
    SESSION_DRIVER=file \
    QUEUE_CONNECTION=sync

# Apache listens on 8080 so it can run without root, and serves public/ with mod_rewrite.
RUN a2enmod rewrite \
 && sed -ri 's/^Listen 80$/Listen 8080/' /etc/apache2/ports.conf \
 && sed -ri 's!<VirtualHost \*:80>!<VirtualHost *:8080>!; s!/var/www/html!/var/www/html/public!' /etc/apache2/sites-available/000-default.conf
# bitploy's proxy ends HTTPS and passes X-Forwarded-Proto: HTTPS=on makes Laravel
# build https:// links and asset URLs without trusting the proxy in bootstrap/app.php.
COPY <<'APACHECONF' /etc/apache2/conf-available/laravel.conf
ServerName localhost
SetEnvIf X-Forwarded-Proto "^https$" HTTPS=on
<Directory /var/www/html/public>
    AllowOverride All
    Require all granted
</Directory>
APACHECONF
RUN a2enconf laravel

WORKDIR /var/www/html
COPY --from=vendor /app ./
COPY --from=assets /app/public/build ./public/build

# Start script: make an app key if none is set, create the database file, migrate,
# cache the config, run Apache.
COPY --chmod=755 <<'STARTSCRIPT' /usr/local/bin/start
#!/bin/sh
set -e
cd /var/www/html
# No APP_KEY in the app's variables: make one once and keep it in /data next to the
# database it encrypts.
if [ -z "$APP_KEY" ]; then
  [ -s /data/app-key ] || (umask 077 && php -r 'echo "base64:", base64_encode(random_bytes(32));' > /data/app-key)
  export APP_KEY="$(cat /data/app-key)"
fi
# Links in emails and queued jobs use APP_URL: default to the app's address.
if [ -z "$APP_URL" ] && [ -n "$BITPLOY_URL" ]; then
  export APP_URL="$BITPLOY_URL"
fi
touch /data/database.sqlite
php artisan migrate --force
php artisan config:cache
exec apache2-foreground
STARTSCRIPT

# Writable by Apache (www-data): storage, bootstrap/cache and the /data volume.
RUN mkdir -p storage/framework/cache storage/framework/sessions storage/framework/views storage/logs bootstrap/cache \
 && mkdir /data \
 && chown -R www-data:www-data storage bootstrap/cache /data /var/run/apache2 /var/lock/apache2 /var/log/apache2

USER www-data
EXPOSE 8080
CMD ["start"]
