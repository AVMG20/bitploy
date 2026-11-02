# go

For a Go program that serves HTTP. The result is a small image with only your binary in it.

- `go.mod` must exist. `go.sum` is used when present. If `go.mod` asks for a newer Go than the `golang:` tag in the Dockerfile, raise the tag.
- The `main` package must be in the top folder. If it lives in `./cmd/server`, change the build line to `go build ... ./cmd/server`.
- Listen on `0.0.0.0` and `$PORT`, for example `http.ListenAndServe(":"+port, nil)`. Do not use `localhost:8080`.
- The final image has no shell or package manager, only your binary, CA certificates and timezone data.
- A bot without a web port: create the app with `type=worker`; `EXPOSE` is then ignored.
- Keeping files: turn on the volume (`create_app` with volume=true for a new app; an existing app: `update_app` volume=true, then `restart_app`) and write only to `/data`. The final image has no shell, so create the folder in the build stage (`RUN mkdir /data`) and copy it over with `COPY --from=build --chown=nonroot:nonroot /data /data`.
- If `EXPOSE` is not your port, change both, or set the port with update_app.
