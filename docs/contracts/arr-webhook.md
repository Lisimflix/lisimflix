# Sonarr and Radarr import notifications (LIB-4)

Sonarr and Radarr tell Lisimflix about imports through their own Webhook connection
(Settings > Connect > Webhook). Lisimflix never calls them back. This endpoint is not part of
the client API in `openapi.json`: its callers are servers, so it sits outside `/api/` and skips
the browser Origin and CSRF checks. It does get the same security headers and `X-Request-Id`.

## Endpoint

- Path: `/hooks/v1/arr`
- Methods: `POST` or `PUT`. Anything else gets 405 with `Allow: POST, PUT`.
- Body: the app's JSON webhook payload, at most 1 MiB and 1,000 paths.

## Authentication

HTTP Basic. The username is the source ID and the password is the token, both printed once by
`lisimflix hook add`. Only the token's SHA-256 is stored. A revoked source stops working on its
next request.

## User-Agent and supported versions

The payload carries no version, so the `User-Agent` product is checked
(`Sonarr/4.0.20.3014 (ubuntu 22.04)`). The app must match the source's kind (a Sonarr token used by
Radarr is refused). Supported: **Sonarr 4**, **Radarr 5 and 6**. Other versions are refused with a
reason, which Sonarr and Radarr show in their UI. Don't override `User-Agent` in the webhook's
custom headers.

## Events

| eventType | Paths queued |
|---|---|
| `Test` | none: 200 `test`, recorded |
| `Download` (import, upgrade, Sonarr's import-complete with `episodeFiles`) | each file's `path` (else the series/movie folder + `relativePath`), plus `deletedFiles[].path` (an upgrade's replaced files) |
| `Rename` | each renamed file's `path` and `previousPath`; with no list, the folder |
| `EpisodeFileDelete` / `MovieFileDelete` | the file |
| `SeriesDelete` / `MovieDelete` | the series/movie folder |
| `Grab`, `Health`, `HealthRestored`, `ApplicationUpdate`, `ManualInteractionRequired`, `SeriesAdd`, `MovieAdded`, and unknown types | none: 200 `ignored`, recorded. Unknown types are ignored so a new event in a later minor release doesn't fail |

A known event without the paths it should carry is a schema error (400).

## Path mapping

Each source has `--map REMOTE=LOCAL` pairs: a folder as Sonarr/Radarr sees it, and the same folder
relative to Lisimflix's media root. The longest match wins, by whole folders (`/tv` doesn't cover
`/tvshows`). Windows paths get forward slashes, and remote paths are cleaned before mapping, so `..`
can't climb out of a mapping. Every mapped path must fall inside one of the source's library folders.

**All or nothing:** if any path in a notification can't be mapped (unmapped, an escape, another
library, relative), the whole notification is rejected with 400 and nothing is queued. Files of one
notification that share a folder become one scan of that folder; duplicates and scopes inside
another scope are dropped.

## Responses

| Status | Body | When |
|---|---|---|
| 202 | `{"result":"queued","receiptId":"rcpt-...","scopes":[{"rootId":"...","path":"..."}]}` | scans queued |
| 200 | `{"result":"test","receiptId":"rcpt-...","scopes":[]}` | `Test` event |
| 200 | `{"result":"ignored","receiptId":"rcpt-...","scopes":[]}` | event that doesn't change files |
| 400 | `{"code":"invalid_request","message":"Notification rejected: <reason>","requestId":"..."}` | bad payload, unsupported app or version, path that can't be mapped or goes through a symlinked folder, body too large |
| 401 | `{"code":"unauthenticated",...}` with `WWW-Authenticate: Basic` | missing, wrong or revoked credentials |
| 405 | none | method other than POST or PUT |
| 429 | `{"code":"rate_limited",...}` | a limit below was reached |
| 503 | `{"code":"source_unavailable",...}` | the scheduler doesn't know the library yet (added under a minute ago); the next scan catches up |

Every authenticated notification is recorded as a receipt (event, app, result, reason, scopes,
request ID); the newest 200 per source are kept. `lisimflix hook receipts --name <name>` lists them.
The app name and reason text stored are capped (64 and 512 bytes).

## Limits

- 60 notifications per source per minute; beyond that, 429. The missed change is found by the file
  watcher or the 6-hourly scan.
- 20 failed sign-ins per peer per minute (IPv4 address or IPv6 /64); beyond that, every request from
  that peer gets 429, including one with the right token.
- The counters are bounded at 4,096 entries per table; when full of live entries, new peers (or
  sources) are refused with 429 rather than the table growing.

## Receipt, not completion

A 202 means the scans are queued, not done. Completion is the library's scan runs with cause `import`
and those scopes, so a receipt never claims an item is playable.

## Logs

`library.hook` lines: INFO for `queued`, `test` and `ignored`; WARN for `rejected`,
`unauthenticated` and `rate_limited`. Fields: `source_id`, `source`, `event`, `app`, `result`,
`reason`, `scopes` (a count), and `peer` for failed sign-ins. Never the token, the `Authorization`
header or the body.
