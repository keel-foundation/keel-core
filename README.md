# core

The core API service: identity, channels, videos, subscriptions, comments, search, and the moderation-facing domain model.

**Status:** demo API. The demo endpoints return deterministic channel and video metadata. PostgreSQL persistence, OIDC auth, subscriptions, and the moderation client contract are not implemented yet.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/health` | Liveness |
| `GET` | `/api/v1/channels/demo` | Demo channel |
| `GET` | `/api/v1/videos/demo` | Demo video metadata |
| `GET` | `/api/v1/demo` | Demo video and channel |

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `TUBEKEEL_CORE_ADDR` | `127.0.0.1:8080` | Listen address |
| `TUBEKEEL_PIPELINE_PUBLIC_URL` | `http://localhost:8081` | Playback base URL |
| `TUBEKEEL_CORS_ORIGINS` | `http://localhost:3000` | Comma-separated allowed origins |

The service binds to localhost by default. Set `TUBEKEEL_CORE_ADDR=0.0.0.0:8080` only when you intentionally need LAN access.

## Run

```sh
go run ./cmd/api
go test ./...
```

## Next

1. Add PostgreSQL migrations for actors, channels, videos, subscriptions.
2. Add OIDC auth and signed session cookies.
3. Add the video metadata API and playback manifest endpoint.
4. Add contract tests generated from `keel-spec/schemas/openapi.yaml`.

## Licensing

AGPL-3.0. See [LICENSE](LICENSE).
