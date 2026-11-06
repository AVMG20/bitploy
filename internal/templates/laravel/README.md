# laravel

For a Laravel app (10 or newer) on Apache with PHP 8.4 and SQLite in `/data`.

- Commit `composer.lock` so the versions stay fixed. `package.json` and `package-lock.json` are used for the Vite build. No `package.json`: delete stage 2 and the `COPY --from=assets` line (the adapted template does this for you).
- Set the app key before the first deploy: run `php artisan key:generate --show` on your machine and store the result with `set_env` as `APP_KEY`. Also set `APP_URL` to the app's address (`BITPLOY_URL` holds it).
- Behind bitploy the app sees http. Trust the proxy so links and redirects use https: in `bootstrap/app.php` add `$middleware->trustProxies(at: '*');` inside `withMiddleware`.
- Turn on the volume before the first deploy. The database is `/data/database.sqlite`; the start script creates it and runs `php artisan migrate --force` on every start.
- The image sets `DB_CONNECTION=sqlite`, `CACHE_STORE=file`, `SESSION_DRIVER=file`, `QUEUE_CONNECTION=sync`. File sessions are lost on redeploy. For logins that survive, set `SESSION_DRIVER=database` with `set_env` (needs the `sessions` table migration). Jobs run right away with `sync`; a `database` queue needs a worker process and there is only one process per app.
- PostgreSQL or MySQL instead (a database added with `add_service`): add the driver in stage 3 before `USER www-data` (`RUN apt-get update && apt-get install -y --no-install-recommends libpq-dev && docker-php-ext-install pdo_pgsql && rm -rf /var/lib/apt/lists/*`; MySQL or MariaDB: `RUN docker-php-ext-install pdo_mysql`). Set `DB_CONNECTION=pgsql` (or `mysql`, `mariadb`) and `DB_URL=${{postgres.DATABASE_URL}}` with `set_env` (Laravel 10 reads `DATABASE_URL`, which `connect_to` already sets). Take `touch /data/database.sqlite` and `php artisan migrate --force` out of the start script and set `before_deploy_command` to `php artisan migrate --force` (`update_app`), so migrations run once per deploy before the new version takes traffic.
- Uploads: `storage/` is lost on redeploy. Use a filesystem disk whose root is `/data/uploads`.
- Other secrets (mail, API keys) come from `set_env`, never from a committed `.env`.
- Apache listens on 8080 (`EXPOSE 8080`). Extra PHP extensions: add `RUN docker-php-ext-install intl` before `USER www-data` (some need `apt-get install` of a `-dev` package first).
