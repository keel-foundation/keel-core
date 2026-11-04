# core

The core API service: identity, channels, videos, subscriptions, comments, search, and the
moderation-facing domain model.

**Status:** UAT-ready, PostgreSQL-backed API. Actors, channels, videos, accounts, and sessions are
stored in PostgreSQL. Email/password registration, email verification, login, logout, password
reset, the public catalog and discovery endpoints, viewer engagement (subscriptions, reactions,
saves, watch history, comments, and replies), the creator studio/media engine, watch
telemetry/creator analytics, notifications, viewer reports/trust records, playlists, ActivityPub
federation, and the session-authenticated creator moderation gateway are implemented. Social
OIDC/OAuth sign-in for Google, Apple, and X, including account linking and PKCE, is implemented.

`gofmt -l .`, `go vet ./...`, and `go test -p 1 ./...` pass against PostgreSQL + pgvector. The
remaining UAT integration work is the `keel-web` data adapter; production classifier models,
externally anchored moderation ledgers, and real billing/payout execution are future work.

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
| `KEELTUBE_DEMO_MEDIA_URL` | web demo sample | Optional seeded demo playback URL |
| `KEELTUBE_WORKER_ADDR` | `127.0.0.1:8083` | Media worker `/health` and `/status` address |
| `KEELTUBE_WEB_PUBLIC_URL` | `http://localhost:3000` | Captions base URL used by the seed |
| `KEELTUBE_EMBEDDINGS_URL` | unset | `keel-embeddings` base URL; unset uses the in-process hash embedder |
| `KEELTUBE_EMBEDDINGS_TOKEN` | unset | Bearer token for `keel-embeddings`; must match the service |
| `KEELTUBE_EMBEDDINGS_MODEL` | `BAAI/bge-small-en-v1.5` | Embedding model id |
| `KEELTUBE_EMBEDDINGS_DIMENSIONS` | `384` | Embedding dimensions; must match the pgvector column |
| `KEELTUBE_EMBEDDINGS_TIMEOUT_SECONDS` | `30` | Embeddings request timeout |
| `KEELTUBE_MEDIA_S3_BUCKET` | unset | S3-compatible bucket; unset disables studio upload routes |
| `KEELTUBE_MEDIA_S3_REGION` | `us-east-1` | Bucket region |
| `KEELTUBE_MEDIA_S3_ENDPOINT` | unset | S3-compatible endpoint for MinIO/R2 |
| `KEELTUBE_MEDIA_S3_ACCESS_KEY_ID` | unset | Optional static S3 access key |
| `KEELTUBE_MEDIA_S3_SECRET_ACCESS_KEY` | unset | Optional static S3 secret |
| `KEELTUBE_MEDIA_S3_SESSION_TOKEN` | unset | Optional S3 session token for temporary credentials |
| `KEELTUBE_MEDIA_S3_USE_PATH_STYLE` | endpoint-dependent | Use path-style bucket addressing |
| `KEELTUBE_MEDIA_S3_PREFIX` | `media` | Object key prefix |
| `KEELTUBE_MEDIA_PUBLIC_URL` | `http://localhost:8080` | Base URL used in playback links |
| `KEELTUBE_MEDIA_PRESIGN_SECONDS` | `900` | Presigned upload/download lifetime |
| `KEELTUBE_MEDIA_UPLOAD_TTL_SECONDS` | `86400` | Upload session lifetime; must exceed the presign lifetime |
| `KEELTUBE_MEDIA_PART_SIZE_MB` | `64` | Multipart upload part size, minimum 5 |
| `KEELTUBE_MEDIA_MAX_UPLOAD_MB` | `20480` | Maximum source file size |
| `KEELTUBE_MEDIA_RETAIN_SOURCE` | `false` | Keep source objects after successful processing |
| `KEELTUBE_MEDIA_JOB_RETENTION_DAYS` | `7` | Prune completed media jobs after this many days |
| `KEELTUBE_MEDIA_MAX_DURATION_SECONDS` | `14400` | Maximum source duration |
| `KEELTUBE_MEDIA_MIN_DURATION_SECONDS` | `1` | Minimum source duration |
| `KEELTUBE_MEDIA_WORKERS` | `2` | In-process media worker count |
| `KEELTUBE_MEDIA_POLL_SECONDS` | `2` | Queue poll interval |
| `KEELTUBE_MEDIA_JOB_TIMEOUT_SECONDS` | `3600` | Per-job processing timeout |
| `KEELTUBE_MEDIA_JOB_LEASE_SECONDS` | `1800` | Stale job reclaim lease |
| `KEELTUBE_MEDIA_FFMPEG` | `ffmpeg` | ffmpeg binary |
| `KEELTUBE_MEDIA_FFPROBE` | `ffprobe` | ffprobe binary |
| `KEELTUBE_MEDIA_WORK_DIR` | system temp | Scratch directory for processing |
| `KEELTUBE_MEDIA_MAX_RENDITION_HEIGHT` | `1080` | Highest HLS rendition to generate |
| `KEELTUBE_MEDIA_HLS_SEGMENT_SECONDS` | `6` | HLS segment length |
| `KEELTUBE_MEDIA_ALLOWED_EXTENSIONS` | common video extensions | Upload extension allowlist |
| `KEELTUBE_MEDIA_ALLOWED_CONTENT_TYPES` | common video MIME types | Upload MIME allowlist |
| `KEELTUBE_MEDIA_POLICY_TERMS` | unset | Comma-separated metadata block terms |
| `KEELTUBE_MEDIA_SCANNER_URL` | unset | Optional external copyright/policy scanner |
| `KEELTUBE_MEDIA_SCANNER_TOKEN` | unset | Bearer token for the external scanner |
| `KEELTUBE_MEDIA_SCANNER_TIMEOUT_SECONDS` | `10` | External scanner timeout |
| `KEELTUBE_RERANKER_URL` | unset | Optional cross-encoder reranker for semantic search; unset uses the lexical reranker |
| `KEELTUBE_RERANKER_TOKEN` | unset | Optional bearer token for the reranker |
| `KEELTUBE_RERANKER_MODEL` | unset | Optional reranker model identifier |
| `KEELTUBE_RERANKER_TIMEOUT_SECONDS` | `5` | Reranker request timeout |
| `KEELTUBE_ANALYTICS_RETENTION_DAYS` | `90` | Raw view/discovery event retention; daily aggregates remain |
| `KEELTUBE_INTERNAL_TOKEN` | unset | Optional bearer token for the internal notification-ingest endpoint; unset disables the route |
| `KEELTUBE_MODERATION_URL` | unset | `keel-moderation` base URL; unset returns 503 from the creator moderation gateway |
| `KEELTUBE_MODERATION_TOKEN` | unset | Static server-side creator-role bearer token for `keel-moderation`; used only when claims are disabled |
| `KEELTUBE_MODERATION_CLAIMS_SECRET` | unset | Shared HS256 secret for OIDC-style claims; at least 32 characters; enables claims mode |
| `KEELTUBE_MODERATION_CLAIMS_ISSUER` | `keel-core` | Claims `iss` value |
| `KEELTUBE_MODERATION_CLAIMS_AUDIENCE` | `keel-moderation` | Claims `aud` value |
| `KEELTUBE_MODERATION_CLAIMS_TTL_SECONDS` | `60` | Claims lifetime |
| `KEELTUBE_MODERATION_TIMEOUT_SECONDS` | `10` | Timeout for a single moderation request |
| `KEELTUBE_MODERATION_SIGNAL_TOKEN` | unset | Server-side token for viewer-report ingestion into `keel-moderation` `/v1/signals`; unset disables `POST /api/v1/reports` |
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
| `KEELTUBE_FEDERATION_DOMAIN` | unset | Enables ActivityPub federation for this instance |
| `KEELTUBE_FEDERATION_BASE_URL` | `https://<domain>` | Public base URL for federation documents |
| `KEELTUBE_FEDERATION_ACTOR_ID` | `instance` | Instance actor ID |
| `KEELTUBE_FEDERATION_POLICY_VERSION` | `demo-0.1` | Published policy version |
| `KEELTUBE_FEDERATION_TIMEOUT_SECONDS` | `10` | Remote actor/delivery timeout |
| `KEELTUBE_FEDERATION_DELIVERY_LEASE_SECONDS` | `300` | Delivery worker lease |
| `KEELTUBE_FEDERATION_ALLOW_PRIVATE` | `false` | Allow private-network remote actors for local testing |
| `KEELTUBE_SESSION_COOKIE` | `keeltube_session` | Session cookie name |
| `KEELTUBE_CSRF_COOKIE` | `keeltube_csrf` | CSRF cookie name |
| `KEELTUBE_COOKIE_SECURE` | `false` | Set `true` behind HTTPS |
| `KEELTUBE_COOKIE_DOMAIN` | unset | Optional cookie domain |
| `KEELTUBE_SOCIAL_CALLBACK_BASE_URL` | `http://localhost:8080` | Public core URL used for provider callbacks |
| `KEELTUBE_SOCIAL_STATE_TTL_SECONDS` | `600` | OAuth state lifetime, 60-3600 seconds |
| `KEELTUBE_GOOGLE_CLIENT_ID` | unset | Enables Google sign-in |
| `KEELTUBE_GOOGLE_CLIENT_SECRET` | unset | Google OAuth client secret |
| `KEELTUBE_APPLE_CLIENT_ID` | unset | Enables Apple sign-in |
| `KEELTUBE_APPLE_CLIENT_SECRET` | unset | Static Apple client secret; otherwise use the TEAM_ID/KEY_ID/PRIVATE_KEY pair |
| `KEELTUBE_APPLE_TEAM_ID` | unset | Apple developer team ID for generated client secrets |
| `KEELTUBE_APPLE_KEY_ID` | unset | Apple private key ID for generated client secrets |
| `KEELTUBE_APPLE_PRIVATE_KEY` | unset | Apple `.p8` private key contents with escaped newlines |
| `KEELTUBE_APPLE_PRIVATE_KEY_PATH` | unset | Path to the Apple `.p8` private key |
| `KEELTUBE_X_CLIENT_ID` | unset | Enables X sign-in |
| `KEELTUBE_X_CLIENT_SECRET` | unset | X OAuth 2.0 client secret |

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


## Social sign-in

Social sign-in is disabled unless a provider client ID is configured. Google and Apple use OIDC ID
token verification against the provider JWKS; X uses OAuth 2.0 with PKCE and the X user-info
endpoint. Authorization state is stored server-side and is single-use, so a callback cannot be
replayed. Accounts created from a provider have no password; verified provider emails link to an
existing password account, while unverified or missing emails never claim another account and use a
synthetic local address.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/auth/social/providers` | List configured providers |
| `GET` | `/api/v1/auth/social/{provider}/start` | Start sign-in and redirect to the provider |
| `GET` | `/api/v1/auth/social/{provider}/callback` | Complete a query-string callback |
| `POST` | `/api/v1/auth/social/{provider}/callback` | Complete an Apple `form_post` callback |

`start` accepts `return_to` for a same-origin path and returns a provider redirect. Add
`format=json` to receive `{"authorization_url": "...", "expires_at": "..."}` instead. Callbacks set
the normal session and CSRF cookies and redirect back to `KEELTUBE_APP_URL` plus `return_to`, or
return the session JSON when `format=json` is supplied. Register these callback URLs with each
provider:

```text
http://localhost:8080/api/v1/auth/social/google/callback
http://localhost:8080/api/v1/auth/social/apple/callback
http://localhost:8080/api/v1/auth/social/x/callback
```

Set `KEELTUBE_SOCIAL_CALLBACK_BASE_URL` to the public core origin in non-local deployments. Apple
requires either `KEELTUBE_APPLE_CLIENT_SECRET` or all of `KEELTUBE_APPLE_TEAM_ID`,
`KEELTUBE_APPLE_KEY_ID`, and the private key. Provider endpoints and claim names can be overridden
with `KEELTUBE_<PROVIDER>_ISSUER`, `_AUTH_URL`, `_TOKEN_URL`, `_JWKS_URL`, `_USERINFO_URL`,
`_SCOPES`, `_EMAIL_CLAIM`, `_SUBJECT_CLAIM`, `_NAME_CLAIM`, and `_AVATAR_CLAIM`.

## Run locally

Copy the example environment file and fill in your own database connection and mail provider:

```sh
cp .env.example .env
# edit .env
go run ./cmd/seed
go run ./cmd/api
go run ./cmd/worker   # separate process; requires KEELTUBE_MEDIA_S3_*
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

## Search

`GET /api/v1/search` is catalog search and accepts `channel_id`. `GET /api/v1/search/semantic`
embeds the query with the discovery embedder and ranks public videos with pgvector. When
`KEELTUBE_RERANKER_URL` is set, semantic results are reranked by an external cross-encoder; otherwise
a deterministic lexical reranker is used. The reranker receives
`{"query":"...","documents":["..."]}` and returns `{"scores":[0.0]}` with one score per document. `GET /api/v1/search/suggestions` returns compact video,
short, and channel suggestions. `GET /api/v1/channels?ids=...` supports bulk channel lookup.

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

The private dev stack builds and runs core, the seed, embeddings, moderation, PostgreSQL, and an
optional media worker. Supply the database and mail settings through the stack's environment file.

Removed videos return `410 Gone` with a canonical `video` tombstone that omits playback URLs but
keeps the metadata needed to render the removal notice. Private videos return `404` unless the
caller owns the channel.

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

`POST /api/v1/videos/{id}/view` records watch history and starts a telemetry session. The public
view counter increments only for the first session per viewer and video within 30 minutes; repeat
sessions still contribute watch-time analytics.

## Notifications

Creator notifications are stored in PostgreSQL and scoped to the authenticated actor. The web shell
reads them through the session cookie; no moderation token is involved.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/me/notifications?limit=&offset=&unread=` | List notifications and the unread count |
| `GET` | `/api/v1/me/notifications/unread-count` | Read only the unread count |
| `POST` | `/api/v1/me/notifications/{id}/read` | Mark one own notification read |
| `POST` | `/api/v1/me/notifications/read-all` | Mark all own notifications read |
| `POST` | `/internal/v1/notifications` | Service-to-service notification ingest; requires `KEELTUBE_INTERNAL_TOKEN` |

Core creates notifications for new comments, replies, completed uploads, and failed uploads.
Moderation and future finance services can push creator-safe notifications through the internal
route with idempotency provided by `dedupe_key`. The internal route is not registered when `KEELTUBE_INTERNAL_TOKEN` is
unset. It validates that a supplied `channel_id` belongs to the target actor.

Notification rows carry `id`, `kind`, `title`, `body`, `href`, `read`, `read_at`, and `created_at`.
The page response also includes `unread_count`, so the web client can render the badge without a
second request.

## Creator moderation gateway

Core exposes a session-authenticated gateway for the creator-facing moderation record. The browser
never receives a moderation role token: core authenticates the viewer session, resolves the
creator's channel, and calls `keel-moderation` with a server-side creator credential.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/studio/moderation/summary` | Creator-scoped moderation counts |
| `GET` | `/api/v1/studio/moderation/cases` | Creator-scoped cases (`status`, `limit`, `offset`) |
| `GET` | `/api/v1/studio/moderation/cases/{id}` | Read one creator case |
| `GET` | `/api/v1/studio/moderation/actions` | Creator-scoped actions (`status`, `limit`, `offset`) |
| `GET` | `/api/v1/studio/moderation/actions/{id}` | Read one creator action |
| `GET` | `/api/v1/studio/moderation/appeals` | Creator-scoped appeals (`status`, `limit`, `offset`) |
| `GET` | `/api/v1/studio/moderation/appeals/{id}` | Read one creator appeal |
| `GET` | `/api/v1/studio/moderation/ledger` | Creator-scoped ledger records |
| `GET` | `/api/v1/studio/moderation/ledger/verify` | Verify the complete audit chain |
| `GET` | `/api/v1/studio/moderation/ledger/export?format=jsonl\|csv` | Export creator-scoped records |
| `POST` | `/api/v1/studio/moderation/actions/{id}/appeal` | File an appeal on an active action |

The gateway always derives `channel_id` from the authenticated session and ignores a client-supplied
`channel_id`, so one creator cannot read another creator's record. All unsafe requests use the same
CSRF rules as the rest of the studio surface. `limit` is capped at 100. Upstream `4xx` responses are
forwarded with their creator-safe `detail`; a rejected gateway credential or an unreachable service
returns `502`, and a timeout returns `504`. Ledger exports preserve `Content-Type` and
`Content-Disposition`.

Set `KEELTUBE_MODERATION_URL` to enable the gateway. The preferred credential is
`KEELTUBE_MODERATION_CLAIMS_SECRET`: core mints short-lived HS256 JWTs with `iss`, `aud`, `sub`,
`role`, and `channel_ids` from the authenticated session, and `keel-moderation` verifies them in
OIDC mode. This removes the static creator channel map and records the real actor as the appellant.
Set `KEELTUBE_MODERATION_TOKEN` instead only when moderation runs in legacy role-token mode; it must
be the creator-role token. Leave the URL unset to keep the gateway disabled; the endpoints then
return `503`.

Cases and actions accept an optional `video_id` filter so the creator app can load the record for
one video without fetching the whole channel record.

## Federation

Set `KEELTUBE_FEDERATION_DOMAIN` to enable the ActivityPub v0.1 surface. Core serves WebFinger,
ActivityPub actor/outbox/followers/following documents, PeerTube-compatible video objects, a signed
instance policy, and a signed inbox. Outbound activities are queued in PostgreSQL and delivered with
RSA-SHA256 HTTP Signatures. Inbound Follow/Undo/Accept/Reject/Create/Update/Delete/Flag activities
are verified, deduplicated, and applied; remote videos are imported into the catalog only after the
instance follows and the peer accepts.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/.well-known/webfinger` | Resolve `acct:handle@domain` |
| `GET` | `/federation/actors/{id}` | Actor document and public key |
| `GET` | `/federation/actors/{id}/outbox` | Local public videos as Create activities |
| `GET` | `/federation/actors/{id}/followers` | Followers collection |
| `GET` | `/federation/actors/{id}/following` | Following collection |
| `POST` | `/federation/actors/{id}/inbox` | Signed inbox |
| `GET` | `/federation/videos/{id}` | PeerTube-compatible video object |
| `GET` | `/federation/policy` | Signed instance policy |

Internal operator routes under `/internal/v1/federation` require `KEELTUBE_INTERNAL_TOKEN` and let
operators inspect the instance, follow or unfollow a remote actor, block/unblock a peer, and process
the delivery queue. Configure `KEELTUBE_FEDERATION_BASE_URL` when the public origin differs from
`https://<domain>`.

## Viewer reports

`POST /api/v1/reports` accepts `{"subject_type":"video|channel","subject_id":"...","reason":"...",
"details":"..."}`. Core persists the report, resolves the subject, derives its channel, and
forwards the report to `keel-moderation` as a `user_report` signal using
`KEELTUBE_MODERATION_SIGNAL_TOKEN`. Reports are signals, not decisions. The route is rate-limited by
client IP and does not require a session. Signed-in reporters can read their own report status with
`GET /api/v1/reports/{id}` and `GET /api/v1/me/reports`.

`GET /api/v1/videos/{id}/trust` returns the public creator-safe moderation record for a video.
Moderation events are pushed into the creator notification feed through the internal ingest route.

For the claims path, configure both services with the same 32+ character secret:

```sh
# core
KEELTUBE_MODERATION_URL=http://moderation:8082
KEELTUBE_MODERATION_CLAIMS_SECRET=replace-with-at-least-32-characters
KEELTUBE_MODERATION_CLAIMS_ISSUER=keel-core
KEELTUBE_MODERATION_CLAIMS_AUDIENCE=keel-moderation

# moderation
KEELTUBE_OIDC_ISSUER=keel-core
KEELTUBE_OIDC_AUDIENCE=keel-moderation
KEELTUBE_OIDC_HMAC_SECRET=replace-with-at-least-32-characters
KEELTUBE_OIDC_ALGORITHMS=HS256
```

## Finance (read-only demo)

The devstack seeds a read-only finance record for the demo channel. These endpoints are
session-authenticated and channel-scoped:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/studio/monetization` | Eligibility, RPM, holdback, balance, next payout |
| `GET` | `/api/v1/studio/earnings` | Paginated earnings ledger |
| `GET` | `/api/v1/studio/payouts` | Paginated payout history |

Real billing, settlement, and payout execution are future work. `GET /api/v1/studio/stats` includes
the current balance as `revenue`.

## Watch telemetry and creator analytics

Playback telemetry is stored separately from the public view counter and creator analytics
aggregates.

- Signed-in viewers are identified by actor ID. Anonymous viewers receive an opaque, HttpOnly
  visitor cookie. Raw IP addresses and user agents are never stored for telemetry.
- Starting a playback session deduplicates the public view count per viewer and video for 30
  minutes. Repeat sessions are still recorded for watch-time analytics but do not increment views.
- Players send idempotent progress heartbeats with a server-issued session ID and a client event ID.
  Core computes capped watch-time deltas, ignores large seeks, and records completion once.
- Discovery telemetry accepts batched impression and click events with client event IDs for
  idempotency.
- Daily aggregates track sessions, counted views, watch seconds, completions, impressions, and
  clicks. Completion rate uses completed sessions over total sessions.
- Raw view sessions/events and discovery events are pruned after
  `KEELTUBE_ANALYTICS_RETENTION_DAYS` (default 90). Daily aggregates are permanent.

### Endpoints

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/v1/videos/{id}/view` | Start a playback session and optionally count a view |
| `POST` | `/api/v1/videos/{id}/view/progress` | Send an idempotent playback heartbeat |
| `POST` | `/api/v1/telemetry/events` | Record batched impression and click events |
| `GET` | `/api/v1/studio/analytics/summary` | Creator summary for a date range |
| `GET` | `/api/v1/studio/analytics/timeseries` | Daily creator analytics points |
| `GET` | `/api/v1/studio/videos/{id}/analytics` | Per-video summary and daily points |

Analytics range parameters are `from` and `to` in `YYYY-MM-DD` form. The default range is the last
28 UTC days; the maximum range is 366 days. Creator analytics require the studio owner session.

## Media worker

`cmd/worker` is the only process that runs FFmpeg. It shares the database and object store with the
API but scales independently:

- Downloads each source once per processing attempt.
- Scans, probes, and transcodes from that local copy.
- Renews its job lease while running and fences completion/retry updates by worker and attempt.
- Backs off with jitter when the queue is empty instead of polling every worker every two seconds.
- Exposes `GET /health` and `GET /status` on `KEELTUBE_WORKER_ADDR`; `/status` reports queue depth.
- Prunes completed media jobs in batches after `KEELTUBE_MEDIA_JOB_RETENTION_DAYS`.
- Deletes successful source objects unless `KEELTUBE_MEDIA_RETAIN_SOURCE=true`.

The API process never starts media workers. Scaling HTTP replicas therefore does not multiply
FFmpeg processes or temp-disk usage.

## Creator Studio and media engine

The studio module adds creator-scoped video management, immutable edit versions, resumable
S3-compatible multipart uploads, deterministic copyright/policy checks, and background HLS
transcoding. The media engine lives in `internal/media`; the API process only signs uploads and
serves playback, while `cmd/worker` runs the PostgreSQL-backed job queue. Upload bytes go directly
from the browser to the bucket through short-lived presigned part URLs; core never proxies media
bytes.

A processing job downloads each source exactly once per attempt, then scans, probes, and transcodes it. A retry after a failure starts a new attempt and downloads again; a successful scan never triggers a second transcode download. The
worker renews its job lease while running and uses fencing so a stale worker cannot finish or retry
a reclaimed job. Completed jobs are pruned after `KEELTUBE_MEDIA_JOB_RETENTION_DAYS`, and successful
source objects are deleted unless `KEELTUBE_MEDIA_RETAIN_SOURCE=true`.

The bucket is private. Playback URLs point at core, which checks video access and redirects to a
short-lived presigned S3 GET. HLS manifests are streamed by core and segments are redirected, so
private and unlisted visibility rules apply to every segment request. Subtitle tracks use the same
gateway and become the video's `captions_url` when finalized.

### Studio endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/studio/channel` | Current creator channel |
| `GET` | `/api/v1/studio/videos` | All own videos, including drafts and processing |
| `POST` | `/api/v1/studio/videos` | Create a draft |
| `GET` | `/api/v1/studio/videos/{id}` | Read an own video with rights, subtitles, and versions |
| `PATCH` | `/api/v1/studio/videos/{id}` | Update title, description, visibility, category, license, format, tags |
| `POST` | `/api/v1/studio/videos/{id}/publish` | Publish a ready video |
| `POST` | `/api/v1/studio/videos/{id}/unpublish` | Make a published video private |
| `GET` | `/api/v1/studio/videos/{id}/versions` | List immutable versions |
| `POST` | `/api/v1/studio/videos/{id}/versions` | Append a version |
| `GET` | `/api/v1/studio/videos/{id}/versions/{version_id}` | Read one version |
| `POST` | `/api/v1/studio/videos/{id}/thumbnail` | Create a presigned custom thumbnail upload |
| `POST` | `/api/v1/studio/videos/{id}/thumbnail/complete` | Validate and attach a custom thumbnail |
| `GET` | `/api/v1/studio/videos/{id}/subtitles` | List subtitle tracks |
| `POST` | `/api/v1/studio/videos/{id}/subtitles` | Create a presigned subtitle upload |
| `POST` | `/api/v1/studio/videos/{id}/subtitles/{track_id}/complete` | Finalize a subtitle track |
| `DELETE` | `/api/v1/studio/videos/{id}/subtitles/{track_id}` | Delete a subtitle track |
| `GET` | `/api/v1/studio/stats` | Own-channel counts, views, likes, comments, revenue, recent uploads |
| `POST` | `/api/v1/studio/uploads` | Create a multipart upload intent and presigned part URLs |
| `GET` | `/api/v1/studio/uploads/{id}` | Poll upload, scan, and processing status |
| `GET` | `/api/v1/studio/uploads` | List own upload intents (`status`, `limit`, `offset`) |
| `GET` | `/api/v1/studio/uploads/{id}/scans` | Read copyright/policy scan findings |
| `GET` | `/api/v1/studio/comments` | List comments across own videos for export |
| `POST` | `/api/v1/studio/comments/{id}/pin` | Pin a top-level comment |
| `DELETE` | `/api/v1/studio/comments/{id}/pin` | Unpin a comment |
| `DELETE` | `/api/v1/studio/comments/{id}` | Remove a comment and its replies |
| `PATCH` | `/api/v1/studio/channel` | Update name, description, avatar, banner, links |
| `DELETE` | `/api/v1/studio/videos/{id}` | Archive a video |
| `POST` | `/api/v1/studio/videos/{id}/restore` | Restore an archived video as a draft |
| `POST` | `/api/v1/studio/videos/{id}/purge` | Irreversibly delete an archived video and all media objects |
| `POST` | `/api/v1/studio/videos/{id}/schedule` | Schedule a future publish |
| `DELETE` | `/api/v1/studio/videos/{id}/schedule` | Clear a scheduled publish |
| `POST` | `/api/v1/studio/uploads/{id}/parts` | Refresh presigned part URLs for a long upload |
| `POST` | `/api/v1/studio/uploads/{id}/complete` | Complete multipart upload and enqueue scanning |
| `DELETE` | `/api/v1/studio/uploads/{id}` | Abort an in-progress upload |

`GET /api/v1/studio/videos/{id}` is the creator edit aggregate. It returns the standard video fields
at the top level plus `rights`, `subtitles`, and `versions`, so the editor does not need three
separate round trips. `rights` is present only when the video has a rights declaration.

Subtitle playback is served by `GET /api/v1/media/{id}/subtitles/{track_id}` through the same
private media gateway as HLS and MP4 playback.

All studio endpoints require a session principal and CSRF for unsafe methods. Another creator’s
video returns `404`. Creators cannot set moderation-controlled statuses (`removed`, `demonetized`),
counters, policy versions, content hashes, or public IDs.

### Upload and processing flow

1. `POST /studio/uploads` validates metadata, extension, MIME type, size, declared hash, rights
   declaration, and configured policy terms. It creates a private draft and an S3 multipart upload.
2. The browser uploads parts directly to S3 with the returned presigned URLs. Long uploads can
   refresh part URLs with `POST /studio/uploads/{id}/parts` until the upload session expires.
3. `POST /studio/uploads/{id}/complete` verifies part count and size, completes the multipart
   upload, and enqueues a scan job.
4. The scan job downloads the source, computes SHA-256, probes it with ffprobe, checks the hash
   blocklist, detects duplicate published content on other channels, and runs the configured
   scanner. Scanner errors fail closed and are retried.
5. Allowed or review-only uploads enqueue a transcode job. Blocked uploads remain drafts with a
   policy scan reason.
6. The transcode job generates H.264/AAC HLS renditions, an optimized MP4 fallback, a thumbnail,
   and captions from an embedded subtitle stream, then uploads them to the bucket.
7. Allowed uploads publish automatically. Review-only uploads keep a draft with playback ready for
   the creator. `GET /studio/uploads/{id}` exposes the current state.
8. Subtitle tracks are uploaded separately with `POST /studio/videos/{id}/subtitles`, finalized with
   the `complete` endpoint, and become the video `captions_url`. Deleting the active track clears or
   repoints the URL to the next ready track or embedded captions.
9. Custom thumbnails use `POST /studio/videos/{id}/thumbnail` and the matching `complete` endpoint.
   Completion verifies the image decodes as JPEG/PNG and updates the thumbnail media asset.
10. `POST /studio/videos/{id}/purge` permanently deletes an archived video. It rejects videos with
    running media jobs or active uploads, deletes the entire channel/video object prefix (including
    HLS segments), removes the database rows, and records an audit row in `media_purges`.

HLS renditions are selected up to `KEELTUBE_MEDIA_MAX_RENDITION_HEIGHT` and never upscaled. The
processor runs ffmpeg/ffprobe without a shell, limits source size and duration, writes scratch files
with `0600` permissions, and cleans up temporary directories.

### Bucket requirements

- Keep the bucket private. Do not grant public read.
- Grant the core identity `s3:CreateMultipartUpload`, `s3:UploadPart`, `s3:CompleteMultipartUpload`,
  `s3:AbortMultipartUpload`, `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject`, and `s3:HeadObject`
  on the configured prefix.
- Configure bucket CORS for the web origin with `GET`, `HEAD`, `PUT`, and the `ETag` response header
  so direct multipart uploads and HLS playback work.
- Install `ffmpeg` and `ffprobe` in the runtime image. The repository Dockerfile includes them.

### External copyright/policy scanner

The built-in checks are deterministic: MIME/extension/size/duration validation, SHA-256 blocklist
matches, duplicate-content review, metadata terms, link spam, and control characters. Real
fingerprint matching needs an external corpus. Set `KEELTUBE_MEDIA_SCANNER_URL` to a provider that
accepts the JSON scan input and returns:

```json
{"verdict":"allow|review|block","findings":[{"code":"...","message":"...","severity":"...","kind":"copyright"}]}
```

Provider errors are not treated as approval; the job retries and eventually fails closed.

## Next

1. Wire the `keel-web` data adapters through its Cloudflare Worker BFF.
2. Add direct OIDC token forwarding for non-core moderation clients.
3. Add server-signed creator export bundles and monetization/payout integration.
4. Add production classifier models and public transparency aggregates.

## Licensing

AGPL-3.0. See [LICENSE](LICENSE).
