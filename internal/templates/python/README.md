# python

For a Python web app (Flask, FastAPI, Django, plain `http.server`) or a bot.

- Needs `requirements.txt`. If you use `pyproject.toml`, replace the install lines with `COPY . .` followed by `RUN pip install .`.
- Listen on `0.0.0.0` and the `PORT` environment variable, for example `app.run(host="0.0.0.0", port=int(os.environ.get("PORT", 8000)))`.
  Listening on `localhost` or `127.0.0.1` makes the deploy fail with `unhealthy`.
- Edit `CMD` to your real start command and make sure the server (gunicorn, uvicorn) is in `requirements.txt`. Flask: `["gunicorn", "--bind", "0.0.0.0:8000", "app:app"]` where `app:app` is `file:variable`.
- Bot or queue worker without a web port: create the app with `type=worker`, run the bot in the foreground (`CMD ["python", "bot.py"]`), and store tokens with `set_env`.
- Keeping files (SQLite, uploads): turn on the volume (`update_app` volume=true, then `restart_app`), write only to `/data`, and add `RUN mkdir /data && chown app /data` before `USER app`.
- If `EXPOSE` is not your port, change both, or set the port with update_app.
