# core

The core API service: identity, channels, videos, subscriptions, comments, search, and the
moderation-facing domain model.

**Status:** PostgreSQL-backed demo API. Actors, channels, and videos are stored in PostgreSQL and
seeded with one public demo video plus removed and private fixtures. Subscriptions, comments,
search, and full OIDC auth are not implemented yet.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/health` | Liveness plus database ping |
| `GET` | `/api/v1/channels/demo` | Demo channel (`ch_demo`) |
| `GET` | `/api/v1/channels/{id}` | Channel by id |
| `GET` | `/api/v1/videos/demo` | Demo video (`vid_demo_001`) |
| `GET` | `/api/v1/videos/{id}` | Video by id |
| `GET` | `/api/v1/demo` | Demo video and channel |
| `GET` | `/api/v1/me` | Current dev principal |

`/api/v1/me` uses the dev-only auth adapter. When `KEELTUBE_DEV_ACTOR_TOKEN` is set, a matching
`Authorization: Bearer <token>` is required. This is not production authentication.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `KEELTUBE_CORE_ADDR` | `127.0.0.1:8080` | Listen address |
| `KEELTUBE_DATABASE_URL` | `postgres://keeltube:keeltube-dev@127.0.0.1:5432/keeltube?sslmode=disable` | PostgreSQL DSN |
| `KEELTUBE_CORS_ORIGINS` | `http://localhost:3000` | Comma-separated allowed origins |
| `KEELTUBE_DEV_ACTOR_ID` | `usr_demo` | Dev auth principal |
| `KEELTUBE_DEV_ACTOR_TOKEN` | unset | Optional dev bearer token |
| `KEELTUBE_PIPELINE_PUBLIC_URL` | `http://localhost:8081` | Playback base URL used by the seed |
| `KEELTUBE_WEB_PUBLIC_URL` | `http://localhost:3000` | Captions base URL used by the seed |

## Run locally

Start PostgreSQL, for example with the private dev stack:

```sh
docker compose -f keel-infra/devstack/docker-compose.yml up -d postgres
```

Then seed and run core:

```sh
go run ./cmd/seed
go run ./cmd/api
```

The seed is idempotent and creates `usr_demo`, `ch_demo`, `vid_demo_001`,
`vid_removed_001`, and `vid_private_001`.

## Tests

The database tests require PostgreSQL. Set `KEELTUBE_TEST_DATABASE_URL` and run packages
sequentially so the shared test tables do not overlap:

```sh
KEELTUBE_TEST_DATABASE_URL=postgres://keeltube:keeltube-dev@127.0.0.1:5432/keeltube?sslmode=disable \
  go test -p 1 ./...
```

The CI workflow starts a PostgreSQL service automatically. Without the environment variable, the
database tests skip.

## Docker

The private dev stack builds and runs core, the seed, pipeline, moderation, and PostgreSQL:

```sh
docker compose -f keel-infra/devstack/apps.compose.yml up -d --build
```

The seed service runs once and exits; core starts after it completes.

## Next

1. Add subscriptions, comments, search, and a real creator session.
2. Replace the dev auth adapter with OIDC behind the same interface.
3. Add upload-session endpoints and the moderation client contract.

## Licensing

AGPL-3.0. See [LICENSE](LICENSE).
