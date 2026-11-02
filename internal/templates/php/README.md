# php

For a PHP site or app served by Apache (plain `index.php`, small frameworks, WordPress-style code).

- Your files are served from the top of the folder; `index.php` or `index.html` is the front page.
- Apache listens on port 8080, so `EXPOSE 8080` and the app port stay the same.
- SQLite (`pdo_sqlite`) is already in the image; keep the database file in `/data`. Need another extension such as `gd` or `intl`? Add `RUN docker-php-ext-install intl` before `USER www-data` (some need an `apt-get install` of a `-dev` package first).
- Need Composer packages? Add `COPY --from=composer:2 /usr/bin/composer /usr/bin/composer` and `RUN composer install --no-dev` after `COPY`.
- Write persistent files only to `/data`, only after turning the volume on (`create_app` with volume=true for a new app; an existing app: `update_app` volume=true, then `restart_app`), and add `RUN mkdir /data && chown www-data:www-data /data` before `USER www-data`. Everything else is lost on redeploy (uploads, the SQLite file).
- Secrets (API keys) come from `set_env` and are read with `getenv('NAME')`, not from files.
