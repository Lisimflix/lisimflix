# M1 contract fixtures

Synthetic examples of the public M1 API shapes in
[`docs/contracts/m1-openapi.json`](../../../docs/contracts/m1-openapi.json).
Go (`server/internal/contracts`) round-trips them; the web contract tests use the
generated types. All IDs, tokens and titles are made up. Never put real media,
paths, passwords, cookies or grants here.

| File | Schema |
|---|---|
| `anonymous.json` | `AuthState` (signed out: `viewer` is `null`) |
| `authenticated.json` | `AuthState` (signed in) |
| `movies.json` | `MovieList` |
| `playback.json` | `PlaybackSession` |
| `error.json` | `APIError` |
