# django

For a Django project served by gunicorn, with static files from whitenoise and SQLite in `/data`.

- `requirements.txt` must list `django`, `gunicorn` and `whitenoise`.
- In `CMD`, replace `myproject` with the folder that contains `wsgi.py`. Migrations run on every start.
- Turn on the volume before the first deploy (`create_app` with volume=true for a new app; an existing app: `update_app` volume=true, then `restart_app`). The database is `/data/db.sqlite3`; without the volume it is lost on every redeploy. One container per app, so no Postgres or MySQL server.
- Bitploy sets `PORT`, `BITPLOY_APP` and `BITPLOY_URL` (the public https address). Store `DJANGO_SECRET_KEY` with `set_env`.
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

- Uploads (`MEDIA_ROOT`) must also point into `/data`, for example `/data/media`, and Django does not serve them in production by itself.
- If `EXPOSE` is not your port, change both, or set the port with update_app.
