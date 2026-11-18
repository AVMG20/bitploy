# php

For a PHP site or app served by Apache (plain `index.php`, small frameworks, WordPress-style code).

- Your files are served from the top of the folder; `index.php` or `index.html` is the front page.
- Apache listens on port 8080, so `EXPOSE 8080` and the app port stay the same.
- SQLite (`pdo_sqlite`) is already in the image; keep the database file in `/data`. Need another extension such as `gd` or `intl`? Add `RUN docker-php-ext-install intl` before `USER www-data` (some need an `apt-get install` of a `-dev` package first).
- Composer packages: with a `composer.json` the adapted template adds a stage that runs `composer install --no-dev` (commit `composer.lock` so versions stay the same) and copies the result with `vendor/` into the image.
- Write persistent files only to `/data`, only after turning the volume on, and add `RUN mkdir /data && chown www-data:www-data /data` before `USER www-data`. Everything else is lost on redeploy (uploads, the SQLite file).
- Secrets (API keys) come from `set_env` and are read with `getenv('NAME')`, not from files.
