# generic

A blank starting point when no other template fits (Ruby, Java, Deno, Rust, a bot, a custom server).

- Change `FROM` to an image that contains your language, and pin a version (never `latest`).
- Replace the install step and `CMD` with your real build and start commands.
- Web apps listen on `0.0.0.0` and `$PORT`. A worker (bot, queue consumer) has no port: create the app with type `worker` and delete `EXPOSE`.
- Never copy secrets into the image. Use set_env.
- Keeping files: turn on the volume (`update_app` volume=true, then `restart_app`), write only to `/data`, and add `RUN mkdir /data && chown app /data` before `USER app`.
