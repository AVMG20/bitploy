# python

For a Python web app (Flask, FastAPI, Django, plain `http.server`).

- Needs `requirements.txt`. If you use `pyproject.toml`, replace the install lines with `COPY . .` followed by `RUN pip install .`.
- Listen on `0.0.0.0` and the `PORT` environment variable, for example `app.run(host="0.0.0.0", port=int(os.environ.get("PORT", 8000)))`.
  Listening on `localhost` or `127.0.0.1` makes the deploy fail with `unhealthy`.
- Edit `CMD` to your real start command and make sure the server (gunicorn, uvicorn) is in `requirements.txt`.
- If `EXPOSE` is not your port, change both, or set the port with update_app.
