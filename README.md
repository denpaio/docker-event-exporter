# docker-event-exporter

Streams Docker daemon events and posts **only abnormal container events** to
Slack, so problems surface the moment they happen instead of during a later log
dig.

## What it notifies

| Event | Condition | Severity |
| --- | --- | --- |
| `oom` | Container killed for running out of memory | 🔴 critical |
| `die` | Container exited with a non-zero, non-ignored exit code **without** a preceding stop signal | 🔴 critical |
| `health_status: unhealthy` | Health check flipped to unhealthy | 🟡 warning |

Everything else (start, stop, clean exits, `health_status: healthy`, …) is
ignored. Abnormality is decided **client-side** — the daemon is only asked for
`type=container` events — which sidesteps the historically flaky server-side
`health_status` filter. Repeated identical events for the same container are
de-duplicated within `EVENT_COOLDOWN` (Docker can emit OOM events in bursts).

An intentional stop (Ctrl-C, `docker stop`, `docker compose down`/`restart`) is
**not** a crash: the daemon emits a `kill` (stop signal) event before the `die`,
so any `die` that follows a stop signal within `STOP_GRACE` is suppressed —
whatever exit code the container reports. A container that dies on its own has no
preceding `kill`, so genuine crashes still notify.

## Configuration

All configuration is via environment variables:

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `SLACK_BOT_TOKEN` | ✅ | — | Bot token (`xoxb-…`) with the `chat:write` scope |
| `SLACK_CHANNEL` | ✅ | — | Channel ID (e.g. `C0123456789`) or `#channel-name` |
| `NODE_NAME` | | container hostname | Host label shown in notifications |
| `EVENT_COOLDOWN` | | `30s` | De-duplication window (Go duration) |
| `STOP_GRACE` | | `30s` | Window after a stop signal in which a `die` counts as an intentional stop, not a crash (Go duration; `0` disables) |
| `IGNORE_EXIT_CODES` | | `0` | Extra `die` exit codes to ignore (comma-separated); `0` is always ignored |
| `DOCKER_HOST` | | `unix:///var/run/docker.sock` | Standard Docker env var |

### Reducing redeploy noise

Stops and redeploys are silenced automatically by the stop detection above,
regardless of exit code — including apps that trap `SIGTERM` and exit non-zero
(e.g. exit `1`), which is why filtering by exit code alone can't catch them. Set
`STOP_GRACE` higher than your longest `stop_grace_period` so slow graceful
shutdowns still fall inside the window; set it to `0` to turn detection off.

`IGNORE_EXIT_CODES` remains for the separate case of a container that exits
non-zero **on its own** for a benign reason (e.g. a one-shot job that returns
`2`), where there is no stop signal to key off.

## Slack app setup

1. Create a Slack app → **OAuth & Permissions** → add the bot scope
   `chat:write` (add `chat:write.public` too if you want to post to a channel by
   name without inviting the bot).
2. Install the app to the workspace and copy the **Bot User OAuth Token**
   (`xoxb-…`).
3. Invite the bot to the target channel, or use the channel **ID** directly.

## Running with Docker Compose

```bash
cp .env.example .env      # then fill in SLACK_BOT_TOKEN and SLACK_CHANNEL
docker compose up -d --build
docker compose logs -f
```

The compose file mounts the Docker socket read-only and restarts unless stopped.

## Local development

```bash
go test ./...

# Verify Slack connectivity (sends one test message, then exits):
SLACK_BOT_TOKEN=xoxb-… SLACK_CHANNEL=C0123456789 \
  go run ./cmd/docker-event-exporter -test

# Run against the local daemon:
SLACK_BOT_TOKEN=xoxb-… SLACK_CHANNEL=C0123456789 \
  go run ./cmd/docker-event-exporter
```

Trigger real events to confirm end-to-end:

```bash
# Non-zero exit -> die exit=1 notification
docker run --rm alpine sh -c 'exit 1'

# OOM -> oom notification
docker run --rm --memory=16m --memory-swap=16m \
  polinux/stress stress --vm 1 --vm-bytes 64m --timeout 10s

# Unhealthy -> health_status notification
docker run --rm --health-cmd 'exit 1' --health-interval=5s nginx

# Clean stop -> no notification (noise check)
docker run -d --name noise alpine sleep 3600 && docker stop noise
```

## Security note

Mounting the Docker socket — even read-only — grants full Docker API access to
the container. For tighter isolation, front it with a filtered proxy such as
[`tecnativa/docker-socket-proxy`](https://github.com/Tecnativa/docker-socket-proxy)
exposing only the events endpoint, and point `DOCKER_HOST` at the proxy.

## License

MIT © Denpa Ltd. See [LICENSE](LICENSE).
