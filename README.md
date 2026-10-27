# Lisimflix

A self-hosted, multi-user media server for movies, TV and anime: a Plex/Jellyfin
replacement that anyone can run on their own Linux/Docker host.

**Status:** early development. There is no working server yet. The first milestone (M1)
is playing one 1080p H.264 MP4 movie at its original quality, with no re-encoding, in Chrome
and Firefox on Linux and macOS (Windows follows in a later milestone). The M1 API contract and workspace seed are on `main`.

## Layout

| Path | What |
|---|---|
| `docs/superpowers/specs/` | Design spec: scope, feature catalogue (§9.4), decisions (§14) |
| `docs/superpowers/plans/` | Implementation plans, one per claimed feature |
| `docs/contracts/` | OpenAPI contract (`m1-openapi.json`) |
| `server/` | Go server |
| `web/` | React + Vite web app |
| `testdata/` | Synthetic fixtures (no real media) |

## Checks

Needs Go 1.27.3 and Node 24.22.1 (e.g. via [mise](https://mise.jdx.dev/)).

```sh
npm --prefix web ci
make check
```

`make check` runs gofmt, vet and Go tests, regenerates the API types, then runs the web
contract tests, lint, typecheck and build. GitHub Actions runs the same target on every PR.
It fails if `web/src/api/schema.d.ts` is stale, so commit the regenerated file after
changing `docs/contracts/m1-openapi.json`.

## Contributing

Read [`CONTRIBUTING.md`](CONTRIBUTING.md) first: pick an unclaimed feature, claim its issue,
get the plan approved, then open a PR for review. Agents also follow [`CLAUDE.md`](CLAUDE.md).
