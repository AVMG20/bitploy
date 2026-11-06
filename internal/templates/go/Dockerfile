# Stage 1: compile a static binary.
FROM golang:1.26-alpine AS build
# A module that needs a newer Go than this image makes Go download that version
# instead of failing with "requires go >= ...".
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app .

# Stage 2: copy only the binary into a tiny image that runs as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot

# Your server must listen on 0.0.0.0 and the PORT environment variable (default 8080),
# never on localhost or 127.0.0.1.
EXPOSE 8080
ENTRYPOINT ["/app"]
