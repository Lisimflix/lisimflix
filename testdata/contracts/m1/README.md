# M1 contract fixtures

Synthetic examples of the public M1 API shapes in
[`docs/contracts/openapi.json`](../../../docs/contracts/openapi.json).
Go (`server/internal/contracts`) round-trips them; `npm --prefix web run test:contracts`
validates these same files against OpenAPI and rejects invalid examples. `make check`
runs both checks plus the generated TypeScript assertions. All IDs, tokens and
titles are made up. Never put real media,
paths, passwords, cookies or grants here.

| File | Schema |
|---|---|
| `anonymous.json` | `AuthState` (signed out: `viewer` is `null`) |
| `authenticated.json` | `AuthState` (signed in) |
| `movies.json` | `MovieList` |
| `playback.json` | `PlaybackSession` |
| `error.json` | `APIError` |
