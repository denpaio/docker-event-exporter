# syntax=docker/dockerfile:1

FROM golang:1.26.3-alpine AS build
WORKDIR /src

# Download modules first so they are cached across source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/docker-event-exporter ./cmd/docker-event-exporter

# Runs as root (uid 0) so it can read the mounted docker socket.
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/docker-event-exporter /docker-event-exporter
ENTRYPOINT ["/docker-event-exporter"]
