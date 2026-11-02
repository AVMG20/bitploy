FROM python:3.14-slim
ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1
WORKDIR /app

# Install dependencies first so Docker can cache this layer.
COPY requirements.txt ./
RUN pip install -r requirements.txt

COPY . .

# Collect static files into STATIC_ROOT (served by whitenoise). The key is only used during the build.
RUN DJANGO_SECRET_KEY=build-only python manage.py collectstatic --noinput

# Run as an unprivileged user. The SQLite database lives in /data.
RUN useradd --system --uid 10001 --create-home app \
 && mkdir /data && chown app /data
USER app

# gunicorn listens on 0.0.0.0 and port 8000, never on localhost or 127.0.0.1.
EXPOSE 8000

# Replace "myproject" with the folder that contains settings.py and wsgi.py.
# Migrations run on every start, before the server.
CMD ["sh", "-c", "python manage.py migrate --noinput && exec gunicorn myproject.wsgi:application --bind 0.0.0.0:8000 --workers 2"]
