# django

For a Django project served by gunicorn, with static files from whitenoise and SQLite in `/data`.

- `requirements.txt` must list `django`, `gunicorn` and `whitenoise`.
- In `CMD`, replace `myproject` with the folder that contains `wsgi.py`. Migrations run on every start.
- Turn on the volume before the first deploy. The database is `/data/db.sqlite3`; without the volume it is lost on every redeploy.
- bitploy sets `PORT`, `BITPLOY_APP` and `BITPLOY_URL` (the public https address). Store `DJANGO_SECRET_KEY` with `set_env`.
- Changes in `settings.py`:

```python
import os
from urllib.parse import urlparse

SECRET_KEY = os.environ.get("DJANGO_SECRET_KEY", "dev-only-key")
DEBUG = os.environ.get("DJANGO_DEBUG") == "1"

APP_URL = os.environ.get("BITPLOY_URL", "")
ALLOWED_HOSTS = ["localhost", "127.0.0.1"] + ([urlparse(APP_URL).hostname] if APP_URL else [])
ALLOWED_HOSTS += [h for h in os.environ.get("DJANGO_ALLOWED_HOSTS", "").split(",") if h]  # custom domains
CSRF_TRUSTED_ORIGINS = [APP_URL] if APP_URL else []
SECURE_PROXY_SSL_HEADER = ("HTTP_X_FORWARDED_PROTO", "https")

DATABASES = {"default": {
    "ENGINE": "django.db.backends.sqlite3",
    "NAME": "/data/db.sqlite3" if os.path.isdir("/data") else BASE_DIR / "db.sqlite3",
}}

STATIC_URL = "static/"
STATIC_ROOT = BASE_DIR / "staticfiles"
# add "whitenoise.middleware.WhiteNoiseMiddleware" right after SecurityMiddleware in MIDDLEWARE
```

- PostgreSQL instead (a database added with `add_service`, which sets `DATABASE_URL`): add `psycopg[binary]` and `dj-database-url` to `requirements.txt`, use `DATABASES = {"default": dj_database_url.config()}`, take `python manage.py migrate --noinput &&` out of `CMD` and set `before_deploy_command` to `python manage.py migrate --noinput` (`update_app`), so migrations run once per deploy before the new version takes traffic.
- Uploads (`MEDIA_ROOT`) must also point into `/data`, for example `/data/media`, and Django does not serve them in production by itself.
- If `EXPOSE` is not your port, change both, or set the port with update_app.
