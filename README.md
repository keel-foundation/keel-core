# core

The core API service: identity, channels, videos, subscriptions, comments, search, and the moderation-facing domain model.

**Status:** scaffold. Only `/health` exists.

## Responsibilities

- Actor and channel identity (OIDC, keys, portable IDs)
- Video metadata and playback manifests
- Subscriptions, feed, comments, search
- Moderation domain objects (cases, actions, appeals) exposed to the moderation service
- Export/import orchestration

## Layout

```
cmd/api/          HTTP entrypoint
internal/         domain packages (to be added)
```

## Run

```sh
go run ./cmd/api
# TUBEKEEL_CORE_ADDR=:8080 by default
```

## Next

1. Add PostgreSQL migrations for actors, channels, videos, subscriptions.
2. Add OIDC auth and signed session cookies.
3. Add the video metadata API and playback manifest endpoint.
4. Add contract tests for the moderation case/action/appeal boundary.
