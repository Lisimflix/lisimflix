# Lisimflix

A self-hosted, multi-user media server for movies, TV and anime: a Plex/Jellyfin
replacement that anyone can run on their own Linux/Docker host.

**Status:** early development. The first milestone (M1) is playing one 1080p H.264 MP4
movie at its original quality, with no re-encoding, in Chrome and Firefox on Linux and macOS
(Windows follows in a later milestone). The M1 server, web player and container are on `main`,
and playback passes on all four targets (#19).

To run it, see [`deploy/README.md`](deploy/README.md). M1 is for a trusted private network
only: plain HTTP, one viewer and one movie.

## Layout

| Path | What |
|---|---|
| `docs/superpowers/specs/` | Design spec: scope, feature catalogue (§9.4), decisions (§14) |
| `docs/superpowers/plans/` | Implementation plans, one per claimed feature |
| `docs/contracts/` | OpenAPI contract (`m1-openapi.json`) |
| `docs/testing/` | Test evidence templates and reference installations |
| `deploy/` | Container compose file and run guide |
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
get the plan approved, then open a PR for review. Keep this README current: a PR that changes
what it describes updates it in the same PR. Agents also follow [`CLAUDE.md`](CLAUDE.md).
