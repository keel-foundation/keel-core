# core

The core API service: identity, channels, videos, subscriptions, comments, search, and the
moderation-facing domain model.

**Status:** PostgreSQL-backed demo API. Actors, channels, videos, accounts, and sessions are stored
in PostgreSQL. Email/password registration, email verification, login, logout, password reset, the
public catalog and discovery endpoints, and viewer engagement (subscriptions, reactions, saves,
watch history, comments, and replies) are implemented. Social providers and full OIDC are not
implemented yet.

## Configuration

All settings come from the environment. For standalone local development, copy `.env.example` to
`.env` in this repository and fill in your own values. When running the backend stack through
`keel-infra/devstack/apps.compose.yml`, use `keel-infra/devstack/.env` as the single source of truth
and do not keep a second real `.env` here.

`.env` is ignored by git and must never be committed. `KEELTUBE_DATABASE_URL` is required; the
service has no built-in credentials or connection defaults.

| Variable | Default | Purpose |
|---|---|---|
| `KEELTUBE_CORE_ADDR` | `127.0.0.1:8080` | Listen address |
| `KEELTUBE_DATABASE_URL` | required | PostgreSQL DSN |
| `KEELTUBE_CORS_ORIGINS` | `http://localhost:3000` | Comma-separated allowed origins |
| `KEELTUBE_APP_URL` | `http://localhost:3000` | Base URL for password-reset links |
| `KEELTUBE_EMAIL_LOGO_URL` | embedded lockup | Optional light-mode logo override |
| `KEELTUBE_EMAIL_LOGO_DARK_URL` | embedded dark lockup | Optional dark-mode logo override |
| `KEELTUBE_DEV_ACTOR_ID` | `usr_demo` | Dev fallback principal |
| `KEELTUBE_DEV_ACTOR_TOKEN` | unset | Optional dev bearer token |
| `KEELTUBE_AUTH_ALLOW_DEV` | enabled when the dev token is set | Allow the fixed dev principal |
| `KEELTUBE_PIPELINE_PUBLIC_URL` | `http://localhost:8081` | Playback base URL used by the seed |
| `KEELTUBE_WEB_PUBLIC_URL` | `http://localhost:3000` | Captions base URL used by the seed |
| `KEELTUBE_EMBEDDINGS_URL` | unset | `keel-embeddings` base URL; unset uses the in-process hash embedder |
| `KEELTUBE_EMBEDDINGS_TOKEN` | unset | Bearer token for `keel-embeddings`; must match the service |
| `KEELTUBE_EMBEDDINGS_MODEL` | `BAAI/bge-small-en-v1.5` | Embedding model id |
| `KEELTUBE_EMBEDDINGS_DIMENSIONS` | `384` | Embedding dimensions; must match the pgvector column |
| `KEELTUBE_EMBEDDINGS_TIMEOUT_SECONDS` | `30` | Embeddings request timeout |
| `KEELTUBE_SEED_DEMO_EMAIL` | unset | Optional demo account email |
| `KEELTUBE_SEED_DEMO_PASSWORD` | unset | Optional demo account password |
| `KEELTUBE_MAIL_PROVIDER` | `mailtrap` | `smtp`, `mailtrap`, `mailgun`, or `log` |
| `KEELTUBE_MAIL_FROM` | `KeelTube <no-reply@keeltube.local>` | From address |
| `KEELTUBE_MAIL_TIMEOUT_SECONDS` | `10` | Mailer timeout |
| `KEELTUBE_SMTP_HOST` | unset | SMTP host (required for `smtp`) |
| `KEELTUBE_SMTP_PORT` | `587` | SMTP port |
| `KEELTUBE_SMTP_USERNAME` | unset | SMTP username (required for `smtp`) |
| `KEELTUBE_SMTP_PASSWORD` | unset | SMTP password (required for `smtp`) |
| `KEELTUBE_SMTP_SECURITY` | `starttls` | `starttls`, `ssl`, or `none` |
| `KEELTUBE_MAIL_API_KEY` | unset | Mailtrap/Mailgun API key; unset uses the log mailer |
| `KEELTUBE_MAIL_DOMAIN` | unset | Mailgun sending domain |
| `KEELTUBE_MAIL_BASE_URL` | provider default | Optional Mailtrap/Mailgun base URL override |
| `KEELTUBE_SESSION_COOKIE` | `keeltube_session` | Session cookie name |
| `KEELTUBE_CSRF_COOKIE` | `keeltube_csrf` | CSRF cookie name |
| `KEELTUBE_COOKIE_SECURE` | `false` | Set `true` behind HTTPS |
| `KEELTUBE_COOKIE_DOMAIN` | unset | Optional cookie domain |

### SMTP

Set `KEELTUBE_MAIL_PROVIDER=smtp` and fill the SMTP values:

```sh
KEELTUBE_MAIL_PROVIDER=smtp
KEELTUBE_SMTP_HOST=smtp.example.com
KEELTUBE_SMTP_PORT=587
KEELTUBE_SMTP_USERNAME=your-smtp-username
KEELTUBE_SMTP_PASSWORD=your-smtp-password
KEELTUBE_SMTP_SECURITY=starttls
KEELTUBE_MAIL_FROM="KeelTube <no-reply@example.com>"
```

Use `KEELTUBE_SMTP_SECURITY=ssl` for implicit TLS on port 465, or `none` for a local relay. SMTP
requires both the username and password.

## Run locally

Copy the example environment file and fill in your own database connection and mail provider:

```sh
cp .env.example .env
# edit .env
go run ./cmd/seed
go run ./cmd/api
```

This standalone workflow is optional. The maintainer devstack uses
`keel-infra/devstack/.env` and passes it to the containers.

The seed is idempotent. It creates the demo actor, channel, and video fixtures plus the catalog
channels and videos used by discovery. The demo account is only created when both
`KEELTUBE_SEED_DEMO_EMAIL` and `KEELTUBE_SEED_DEMO_PASSWORD` are set.

## Tests

The database tests require PostgreSQL. Set `KEELTUBE_TEST_DATABASE_URL` in your environment and run
packages sequentially so the shared test tables do not overlap:

```sh
export KEELTUBE_TEST_DATABASE_URL='postgres://USER:PASSWORD@HOST:PORT/DATABASE?sslmode=disable'
go test -p 1 ./...
```

The CI workflow starts a PostgreSQL service automatically. Without the environment variable, the
database tests skip.

## Discovery

`GET /api/v1/feed` personalizes when the request carries a session and the viewer has signals.
`GET /api/v1/explore` returns personalized sections with reasons for the same viewers. Anonymous
and new viewers get a trending, latest, and category-diverse cold-start feed plus the editorial
Explore collections.

Signals are watch history, likes, saves, and subscriptions. The engine stores video and actor
embeddings in PostgreSQL using `pgvector` (`vector(384)`), ranks candidates with cosine similarity,
subscription and category affinity, popularity, recency, and a small recently-watched penalty, and
applies channel/category diversity. Curated sections include Recently watched, Because you
watched/liked/saved X, More in a category, and New from your subscriptions.

Video embeddings are refreshed by the seed and lazily before discovery requests. Actor embeddings
are cached and invalidated when the viewer watches, reacts, saves, subscribes, or unsubscribes. If
`keel-embeddings` is unavailable, discovery falls back to the cold-start feed and editorial Explore.
Leave `KEELTUBE_EMBEDDINGS_URL` unset to use the deterministic hash embedder for local development.
The PostgreSQL image must provide the `vector` extension; changing the model dimension requires a
matching migration.

## Docker

The private dev stack builds and runs core, the seed, embeddings, pipeline, moderation, and
PostgreSQL. Supply the database and mail settings through the stack's environment file.

## Engagement

Authenticated viewer state is stored in PostgreSQL and scoped to the session principal. These
endpoints require CSRF validation when the request uses a session cookie. Public comment reads
remain available to anonymous viewers.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/channels/{id}/subscription` | Read the current subscription state |
| `POST` | `/api/v1/channels/{id}/subscription` | Subscribe to a channel |
| `DELETE` | `/api/v1/channels/{id}/subscription` | Unsubscribe from a channel |
| `GET` | `/api/v1/me/subscriptions` | List subscribed channels |
| `GET` | `/api/v1/me/feed` | List public videos from subscribed channels |
| `GET` | `/api/v1/videos/{id}/reaction` | Read the current reaction and like count |
| `PUT` | `/api/v1/videos/{id}/reaction` | Set a `like` or `dislike` reaction |
| `DELETE` | `/api/v1/videos/{id}/reaction` | Clear the current reaction |
| `GET` | `/api/v1/videos/{id}/save` | Read the saved state |
| `PUT` | `/api/v1/videos/{id}/save` | Save a video |
| `DELETE` | `/api/v1/videos/{id}/save` | Remove a saved video |
| `POST` | `/api/v1/videos/{id}/view` | Record a view and update watch history |
| `GET` | `/api/v1/me/library?tab=saved\|liked\|history` | Read the personal library |
| `DELETE` | `/api/v1/me/history` | Clear watch history |
| `GET` | `/api/v1/videos/{id}/comments` | List visible comments and replies |
| `POST` | `/api/v1/videos/{id}/comments` | Post a comment or one-level reply |
| `POST` | `/api/v1/comments/{id}/like` | Like a comment |
| `DELETE` | `/api/v1/comments/{id}/like` | Remove a comment like |

`POST /api/v1/videos/{id}/view` records watch history and increments the public view counter for
each authenticated request. Time-window view deduplication is future work.

## Next

1. Add creator studio write endpoints (metadata updates, status and visibility, versions) and an
   upload handshake.
2. Replace the dev fallback with full OIDC behind the same authenticator interface.
3. Connect approved Google, Apple, and X providers.
4. Add per-creator scoping to the moderation client contract.
5. Add report ingestion to the moderation service, not core.

## Licensing

AGPL-3.0. See [LICENSE](LICENSE).
