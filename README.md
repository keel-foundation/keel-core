# core

The core API service: identity, channels, videos, subscriptions, comments, search, and the
moderation-facing domain model.

**Status:** PostgreSQL-backed demo API. Actors, channels, videos, accounts, and sessions are stored
in PostgreSQL. Email/password registration, email verification, login, logout, password reset, the
public catalog and discovery endpoints, viewer engagement (subscriptions, reactions, saves, watch
history, comments, and replies), and the creator studio/media engine are implemented. Social
providers and full OIDC are not implemented yet.

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
| `KEELTUBE_MEDIA_S3_BUCKET` | unset | S3-compatible bucket; unset disables studio upload routes |
| `KEELTUBE_MEDIA_S3_REGION` | `us-east-1` | Bucket region |
| `KEELTUBE_MEDIA_S3_ENDPOINT` | unset | S3-compatible endpoint for MinIO/R2 |
| `KEELTUBE_MEDIA_S3_ACCESS_KEY_ID` | unset | Optional static S3 access key |
| `KEELTUBE_MEDIA_S3_SECRET_ACCESS_KEY` | unset | Optional static S3 secret |
| `KEELTUBE_MEDIA_S3_USE_PATH_STYLE` | endpoint-dependent | Use path-style bucket addressing |
| `KEELTUBE_MEDIA_S3_PREFIX` | `media` | Object key prefix |
| `KEELTUBE_MEDIA_PUBLIC_URL` | `http://localhost:8080` | Base URL used in playback links |
| `KEELTUBE_MEDIA_PRESIGN_SECONDS` | `900` | Presigned upload/download lifetime |
| `KEELTUBE_MEDIA_UPLOAD_TTL_SECONDS` | `86400` | Upload session lifetime; must exceed the presign lifetime |
| `KEELTUBE_MEDIA_PART_SIZE_MB` | `64` | Multipart upload part size, minimum 5 |
| `KEELTUBE_MEDIA_MAX_UPLOAD_MB` | `20480` | Maximum source file size |
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

## Creator Studio and media engine

The studio module adds creator-scoped video management, immutable edit versions, resumable
S3-compatible multipart uploads, deterministic copyright/policy checks, and background HLS
transcoding. The media engine lives in `internal/media` and runs in-process with a PostgreSQL-backed
job queue. Upload bytes go directly from the browser to the bucket through short-lived presigned
part URLs; core never proxies media bytes.

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
| `GET` | `/api/v1/studio/videos/{id}` | Read an own video |
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
| `GET` | `/api/v1/studio/stats` | Own-channel counts, views, likes, comments, recent uploads |
| `POST` | `/api/v1/studio/uploads` | Create a multipart upload intent and presigned part URLs |
| `GET` | `/api/v1/studio/uploads/{id}` | Poll upload, scan, and processing status |
| `GET` | `/api/v1/studio/uploads/{id}/scans` | Read copyright/policy scan findings |
| `POST` | `/api/v1/studio/uploads/{id}/parts` | Refresh presigned part URLs for a long upload |
| `POST` | `/api/v1/studio/uploads/{id}/complete` | Complete multipart upload and enqueue scanning |
| `DELETE` | `/api/v1/studio/uploads/{id}` | Abort an in-progress upload |

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

1. Replace the dev fallback with full OIDC behind the same authenticator interface.
2. Connect approved Google, Apple, and X providers.
3. Add per-creator scoping to the moderation client contract.
4. Add report ingestion to the moderation service, not core.
5. Add server-signed creator export bundles and monetization/payout integration.

## Licensing

AGPL-3.0. See [LICENSE](LICENSE).
