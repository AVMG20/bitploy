# Starting point for anything else: a bot, a Ruby or Java app, a custom server.
# Pick a base image that has your language, for example ruby:3.4-slim, eclipse-temurin:25-jre, or deno:2.
FROM alpine:3.22
WORKDIR /app

# Install what the app needs.
RUN apk add --no-cache ca-certificates

COPY . .

# Run as an unprivileged user.
RUN adduser -D -u 10001 app
USER app

# A web app must listen on 0.0.0.0 and the PORT environment variable (default 8080),
# never on localhost or 127.0.0.1. Delete the EXPOSE line for a worker with no web port.
EXPOSE 8080

# Replace with the command that starts your app.
CMD ["./start.sh"]
