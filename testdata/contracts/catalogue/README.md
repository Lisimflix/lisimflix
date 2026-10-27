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

The `sourceRevision` values are real `contracts.SourceRevision` outputs for the
synthetic file IDs, sizes and times, e.g. `fil-synthetic-0001`, 24000000000 bytes,
mtime 1761523200000000000 ns.
