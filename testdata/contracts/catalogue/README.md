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
| `libraries.json` | `LibraryList` | A movie library (idle scan, `partial` availability) and an anime library (scanning, 1,204 of 3,410 files, `available`) |
| `items-movies.json` | `ItemPage` | A first page (`total` 412, a next page): a partly watched movie with a resume position (`matched` null), and an unmatched movie whose file is offline (`matched` and `available` false) |
| `items-episodes.json` | `ItemPage` | A later page (`total` null): episodes 1–2 of a season, with `showTitle` and `seasonIndex`. Both live in one file with unknown boundaries, so neither has a duration; episode 1 was marked watched by hand |
| `item-movie.json` | `ItemDetail` | A movie with two versions: a 1080p MP4 and a 4K HDR10 Director's Cut with TrueHD Atmos (audio `profile`), an untagged AC-3 track with no profile and forced PGS subtitles. Version 2 is offline. A viewer's response: both `fileName`s are null. Also `communityRating`, `directors` and `studios` |
| `item-movie-admin.json` | `ItemDetail` | The same movie as an admin sees it: both versions carry a bare `fileName` |
| `item-episode-shared-file.json` | `ItemDetail` | Episode 2: its version's file (`fil-synthetic-0003`) also holds episode 1, and `segment` is null because the boundaries are unknown |
| `item-show.json` | `ItemDetail` | A show: no user state, no duration, no versions, `childCount` 2, `available` null |
| `facets.json` | `LibraryFacets` | Two genres, years 2006–2023, English and Japanese audio, 1080p and 4K, HDR and SDR present |
| `continue-watching.json` | `ItemPage` | A home row: a movie with a resume, and an episode with `showTitle` and a resume. `total` is null |
| `next-up.json` | `ItemPage` | A home row: episode 2 of a second show, fresh state (the first show has an episode in progress, so it is in `continue-watching.json` instead). `total` is null |
| `profiles.json` | `ProfileList` | An unrestricted profile, and a PIN-protected kids profile limited to one library, AU PG and no unrated items |
| `item-metadata.json` | `ItemMetadata` | Provenance per field: a locked manual title, TMDB fields, an NFO year and a locked NFO TMDB ID |

The `sourceRevision` values are real `contracts.SourceRevision` outputs for the
synthetic file IDs, sizes and times, e.g. `fil-synthetic-0001`, 24000000000 bytes,
mtime 1761523200000000000 ns.
