# go

For a Go program that serves HTTP. The result is a small image with only your binary in it.

- `go.mod` must exist. `go.sum` is used when present.
- The `main` package must be in the top folder. If it lives in `./cmd/server`, change the build line to `go build ... ./cmd/server`.
- Listen on `0.0.0.0` and `$PORT`, for example `http.ListenAndServe(":"+port, nil)`. Do not use `localhost:8080`.
- The final image has no shell or package manager, only your binary, CA certificates and timezone data.
- If `EXPOSE` is not your port, change both, or set the port with update_app.
