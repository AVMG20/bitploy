# php

For a PHP site or app served by Apache (plain `index.php`, small frameworks, WordPress-style code).

- Your files are served from the top of the folder; `index.php` or `index.html` is the front page.
- Apache listens on port 8080, so `EXPOSE 8080` and the app port stay the same.
- Need extensions such as `pdo_mysql` or `gd`? Add `RUN docker-php-ext-install pdo_mysql` before `USER www-data`.
- Need Composer packages? Add `COPY --from=composer:2 /usr/bin/composer /usr/bin/composer` and `RUN composer install --no-dev` after `COPY`.
- Write persistent files only to `/data`, only after turning the volume on (`update_app` volume=true, then `restart_app`), and add `RUN mkdir /data && chown www-data:www-data /data` before `USER www-data`. Everything else is lost on redeploy (uploads, SQLite).
- Secrets (database passwords) come from `set_env` and are read with `getenv('NAME')`, not from files.
