# Catalogue contract fixtures (CTR-2)

Synthetic examples of the catalogue, user-data, profile and metadata-provenance
shapes in [`docs/contracts/openapi.json`](../../../docs/contracts/openapi.json).
Go (`server/internal/contracts`) round-trips every file here strictly;
`npm --prefix web run test:contracts` validates them against OpenAPI and rejects
the invalid variants listed in `web/contract-tests/fixtures.test.mjs`. Producers
(LIB-2, META-1, DATA-1, ACC-1) emit these shapes; UI-2 is built against them.
All IDs, titles and provider IDs are made up. Never put real media, paths,
provider keys or personal data here.

| File | Schema | Shows |
|---|---|---|
| `progress-seek.json` | `ProgressEvent` | A seek from 30:30 to 40:00 |
| `progress-duplicate.json` | `ProgressResult` | A duplicate event: not applied, state unchanged |
| `watched-request.json` | `WatchedRequest` | Mark watched, expecting revision 7 |
| `libraries.json` | `LibraryList` | A movie library and an anime library |
| `items-movies.json` | `ItemPage` | A partly watched movie with a resume position, and a next page |
| `items-episodes.json` | `ItemPage` | Episodes 1–2 of a season. Both live in one file with unknown boundaries, so neither has a duration; episode 1 was marked watched by hand |
| `item-movie.json` | `ItemDetail` | A movie with two versions: a 1080p MP4 and a 4K HDR10 Director's Cut with TrueHD Atmos (audio `profile`), an untagged AC-3 track with no profile and forced PGS subtitles |
| `item-episode-shared-file.json` | `ItemDetail` | Episode 2: its version's file (`fil-synthetic-0003`) also holds episode 1, and `segment` is null because the boundaries are unknown |
| `item-show.json` | `ItemDetail` | A show: no user state, no duration, no versions |

The `sourceRevision` values are real `contracts.SourceRevision` outputs for the
synthetic file IDs, sizes and times, e.g. `fil-synthetic-0001`, 24000000000 bytes,
mtime 1761523200000000000 ns.
