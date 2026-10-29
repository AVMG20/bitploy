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
RUN useradd --system --uid 10001 --no-create-home app
USER app

# Your server must listen on 0.0.0.0 and the PORT environment variable (default 8000),
# never on localhost or 127.0.0.1.
EXPOSE 8000

# Change this to your start command. Examples:
#   ["gunicorn", "--bind", "0.0.0.0:8000", "app:app"]            (Flask, add gunicorn to requirements.txt)
#   ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]  (FastAPI, add uvicorn to requirements.txt)
CMD ["python", "app.py"]
