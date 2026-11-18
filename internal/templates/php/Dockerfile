FROM php:8.4-apache

# Apache listens on 8080 so it can run without root. bitploy's proxy ends HTTPS:
# HTTPS=on tells PHP when a request came in over HTTPS, so links use https://.
RUN sed -ri 's/^Listen 80$/Listen 8080/' /etc/apache2/ports.conf \
 && sed -ri 's/<VirtualHost \*:80>/<VirtualHost *:8080>/' /etc/apache2/sites-available/000-default.conf \
 && echo 'SetEnvIf X-Forwarded-Proto "^https$" HTTPS=on' > /etc/apache2/conf-available/bitploy-https.conf \
 && a2enconf bitploy-https

COPY . /var/www/html/
RUN chown -R www-data:www-data /var/www/html /var/run/apache2 /var/lock/apache2 /var/log/apache2

USER www-data
EXPOSE 8080
