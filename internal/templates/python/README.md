# python

For a Python web app (Flask, FastAPI, Django, plain `http.server`) or a bot.

- Needs `requirements.txt`. If you use `pyproject.toml`, replace the install lines with `COPY . .` followed by `RUN pip install .`. For uv or Poetry the adapted template changes the install lines.
- Listen on `0.0.0.0` and the `PORT` environment variable, for example `app.run(host="0.0.0.0", port=int(os.environ.get("PORT", 8000)))`.
  Listening on `localhost` or `127.0.0.1` makes the deploy fail with `unhealthy`.
- The default `CMD ["python", "app.py"]` is only right for a bot or a plain `http.server`. Flask's built-in server and `uvicorn` run through `python app.py` are not for production. For Flask use gunicorn: `["gunicorn", "--bind", "0.0.0.0:8000", "app:app"]` where `app:app` is `file:variable`. For FastAPI use `["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]`. Put gunicorn or uvicorn in `requirements.txt`.
- Django: use the `django` template.
- Bot or queue worker without a web port: create the app with `type=worker`, run the bot in the foreground (`CMD ["python", "bot.py"]`), and store tokens with `set_env`.
- Keeping files (SQLite, uploads): turn on the volume, write only to `/data`, and add `RUN mkdir /data && chown app /data` before `USER app`.
- If `EXPOSE` is not your port, change both, or set the port with update_app.
