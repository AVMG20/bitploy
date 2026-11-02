FROM python:3.14-slim
ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1
WORKDIR /app

# Install dependencies first so Docker can cache this layer.
COPY requirements.txt ./
RUN pip install -r requirements.txt

COPY . .

# Run as an unprivileged user.
RUN useradd --system --uid 10001 --create-home app
USER app

# Your server must listen on 0.0.0.0 and the PORT environment variable (default 8000),
# never on localhost or 127.0.0.1.
EXPOSE 8000

# Change this to your start command. The default only suits a bot or a plain http.server.
# Web frameworks need a production server (add it to requirements.txt):
#   ["gunicorn", "--bind", "0.0.0.0:8000", "app:app"]                (Flask, "app:app" is file:variable)
#   ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]  (FastAPI)
CMD ["python", "app.py"]
