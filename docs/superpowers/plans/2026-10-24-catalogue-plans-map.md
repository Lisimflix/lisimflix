# Catalogue plans map (accounts, libraries, metadata, user data)

**Status:** Map only. The five draft plans it describes are **archived on the git tag
[`drafts/catalogue-2026-10-24`](https://github.com/Lisimflix/lisimflix/tree/drafts/catalogue-2026-10-24/docs/superpowers/plans/drafts)**,
not in `main`, and are **not executable** (see #12, #13). Unclaimed, unapproved and unimplemented.
Realigned on
2026-10-26 to the spec revision on `main` (#6) and the M1 plan set
([CTR-1 map](2026-10-25-ctr-1-m1-contracts.md)). Nothing here assigns work. Contributors
self-claim feature issues per `CONTRIBUTING.md`.

**Spec:** [Design §§3, 6, 8.2–8.4, 9.1, 9.4–9.5, 10, 12](../specs/2026-10-24-lisimflix-design.md)

These plans were written on 2026-10-24 against an earlier phase structure, before the spec
revision and the CTR-1 contract seed (PR #8). They hold useful, mostly tested, detailed work
for Track D (catalogue/identity) features, plus the LIB-1 probe, which is Track B (spec §9.5).
**They are not ready to execute as-is.** Under the
CLAUDE.md feature planning gate, whoever claims one of these feature IDs runs
`superpowers:writing-plans` against it, applies the alignment deltas below, splits out the
claimed feature's tasks, and gets approval before coding.

## Feature mapping

| Draft (on the tag) | Feature IDs → tasks | Gate | Start dependency (real, minimal) |
|---|---|---|---|
| [`acc-1-2-3-accounts`](https://github.com/Lisimflix/lisimflix/blob/drafts/catalogue-2026-10-24/docs/superpowers/plans/drafts/2026-10-24-acc-1-2-3-accounts.md) | **ACC-1**: T1–7, T9 (+ gap below) · **ACC-2**: T8 · **ACC-3**: T10–T13 (invites + Google/Microsoft) | ACC-1/2 P0 G1 · ACC-3 P1 G3 | CTR-1 merged; M1 runtime `auth`/`app` (OPS-1/ACC-1 M1 slice) for integration; OPS-2 storage for persistence |
| [`lib-1-2-3-scanner`](https://github.com/Lisimflix/lisimflix/blob/drafts/catalogue-2026-10-24/docs/superpowers/plans/drafts/2026-10-24-lib-1-2-3-scanner.md) | **LIB-1**: claimed (#45), planned in [`2026-10-27-lib-1-discovery-probe.md`](2026-10-27-lib-1-discovery-probe.md); reuses T1 · **LIB-2**: T3–T7, T8–T10 (name/NFO parsing), T11, T12 · **LIB-3**: version/extras parts of T6 + T9 | LIB-1/2 P0 G1 · LIB-3 P1 G2 | Name/NFO parsing (T8–T10) can start now with fixtures; store/API parts need OPS-2 + CTR-2; scan engine builds on LIB-1 |
| [`meta-1-3-metadata`](https://github.com/Lisimflix/lisimflix/blob/drafts/catalogue-2026-10-24/docs/superpowers/plans/drafts/2026-10-24-meta-1-3-metadata.md) | **META-1**: T1–T8, T10–T16 · **META-3**: T9 (Fanart.tv) | META-1 P0 G1 · META-3 P2 G4 | Scoring/registry/providers (T2–T4, T8, T10) start now with fake providers; store/matcher need LIB-2 + OPS-2 |
| [`meta-2-anime`](https://github.com/Lisimflix/lisimflix/blob/drafts/catalogue-2026-10-24/docs/superpowers/plans/drafts/2026-10-24-meta-2-anime.md) | **META-2**: T1–T13 | P1 G2 | META-1 registry contract; **decision D06** (AniList/AniDB/mapping terms) before provider work |
| [`data-1-2-3-userdata`](https://github.com/Lisimflix/lisimflix/blob/drafts/catalogue-2026-10-24/docs/superpowers/plans/drafts/2026-10-24-data-1-2-3-userdata.md) | **DATA-1**: T0–T3 · **DATA-2**: T4–T6, T8–T11 · ACC-2 consumer: T7 · **DATA-3** (Plex import): T12–T15 | DATA-1/2 P0 G1 · DATA-3 P2 G4 | CTR-2 progress/profile shapes; rules (T1) start now; ACC-2 evaluator for T7 |

**Retired:** the old phase 1 index and plan 1A (core skeleton). They are superseded by CTR-1
(#8), the [OPS-1/ACC-1 runtime plan](2026-10-25-ops-1-m1-runtime.md) and the
[M1 integration plan](2026-10-25-m1-integration.md). 1A's SQLite migration runner became the
starting point for the [OPS-2 plan](2026-10-26-ops-2-durable-storage.md) (#11, #14). The original
1A file is kept in the tag's history at commit `d90a441`
(`git show d90a441:docs/superpowers/plans/2026-10-24-phase1a-core.md`).

## Alignment deltas (apply to every plan above before claiming)

1. **Contracts come from CTR-1, not 1A.** Use `server/internal/contracts`:
   `contracts.Principal{AccountID, ProfileID, AuthSessionID string}`, `WithPrincipal`, and
   its error writer. Errors are `{code, message, requestId}` with the shared code enum (spec §12).
   Remove every use of `authctx`, `httpx`, `app.Module`, `dbtest` and the 1A `config`/`version`
   packages, and replace them with the CTR-1/OPS equivalents once those exist.
2. **IDs are opaque strings in public JSON** (spec §10.1, §12). Internal int64 row keys are
   fine, but API payloads, fixtures and OpenAPI use stable opaque string IDs. Account and
   profile IDs from the M1 config (`/config/m1.json`) must be preserved by later SQLite work.
3. **Auth runtime belongs to OPS-1/ACC-1.** `server/internal/auth` and `server/internal/app`
   are owned by the M1 runtime plan (`auth.Manager`, `Require`, gorilla/csrf, `X-CSRF-Token`,
   `lisimflix_session` cookie). Account plans **extend** that package. They don't create a
   parallel `Service`/`Guard`, and they drop the drafts' own `X-Lisimflix-CSRF` cookie scheme.
   Other modules protect routes with the runtime's `Require`.
4. **Persistence is OPS-2's `server/internal/storage`**, not the drafts' central
   `server/internal/db` and `server/internal/db/migrations/`. Each module keeps its migrations in
   its own package (`server/internal/<module>/migrations/`) and registers them as a
   `storage.Source`. Files are named `YYYYMMDDHHMMSS_<module>_<what>.sql` (agreed in #14).
5. **HTTP shapes live in OpenAPI.** New routes are added to the contract (CTR-2 for
   catalogue/profile/progress) and the TS client is regenerated. Route registration is a
   shared hotspot. Lists use cursors: default 50, max 200. Retryable mutations take
   idempotency keys, and editable metadata/policy uses revision preconditions.
6. **Checks are `make check`** (Go fmt/vet/test + web lint/types/build, Go 1.27.3, Node
   24.22.1), run locally before pushing and in Actions. Replace the plans' `make test`/`make tidy`
   steps. Adding a Go dependency means coordinating the `go.mod` edit with its current owner.
7. **§8.4 header per claimed feature:** feature IDs, owner role, reviewer, file scope,
   consumed/produced contracts, start/module/integration dependencies, standalone test
   command and integration proof. Record "module accepted" and "integrated" separately.
8. **No chi router.** The drafts import `github.com/go-chi/chi/v5`. OPS-1 uses the standard
   library's `http.ServeMux` (Go 1.22+ method and path patterns) with a short dependency allowlist.
   Rewrite routes for `ServeMux`.
9. **JSON is camelCase**, matching CTR-1 (`accountId`, `requestId`). The ACC, LIB and META-1 drafts
   use snake_case tags (`created_at`); change them. DATA already uses camelCase.
10. **Branch/PR per feature:** branch `feat/<feature-id>-<slug>` (e.g. `feat/lib-1-probe`). The PR
   template's Source names the feature ID and acceptance IDs. The author squash-merges after
   review. Agents never merge.

## How these plans reach `main` (procedure)

- **Drafts stay on the tag.** Only this map is in `main` (#13). When someone claims a feature,
  its executable plan goes into `plans/` in its own small `docs/` PR, written through the planning
  gate. Copy what's useful from the tagged draft; don't merge the draft itself.
- **Claim first.** Open or claim the feature's issue (area labels from the §9.4 table), record
  feature ID, reviewer, file scope, consumed/produced contracts and dependencies, then
  assign yourself before editing the plan for execution.
- **Respect active scopes.** Don't edit files in another person's open PR or assigned issue,
  e.g. CTR-1 (#7/#8) owns `.gitignore`, `Makefile`, `server/go.mod`, `web/`, `.github/` for now.
  Don't edit Pete's M1 plans or the spec. Open an issue instead.
- **Decisions and hand-offs go in issues/PR comments**, not only in chat (e.g. D06 and D07 in
  spec §14, and the gaps below).
- Docs-only PRs may skip local `make check`. Actions still run on the PR.

## Known gaps against the revised spec

- **ACC-1:** admin-created **local username/password users** (spec §6.1, D07). The accounts plan
  only signs non-admin users in through OAuth. Add local user creation, password login for all
  accounts, and password reset by admin.
- **ACC-2 / DATA:** one shared policy evaluator (spec §10.2). The user-data plan's access check
  (T7) must consume ACC-2's evaluator, not re-implement it. D08 proposes kids' profiles deny
  unrated content by default, and that only the account controller manages profiles (this
  matches the accounts plan's "primary profile manages" rule).
- **LIB-4:** optional authenticated Sonarr/Radarr import notifications (P0 G1 library
  milestone) are not planned yet.
- **LIB-2 / §3.6:** check scan revisions, resumable scans and offline-root states against
  §3.6 and SCAN-01/SCALE-01–02. The scanner plan has offline-root safety and stable-size checks.
- **META-1 / §3.7:** provenance, revision and confidence for matches and field locks. Check
  the metadata store's schema covers them.
- **DATA-1 / §3.8, §10.3:** ordered progress events with revisions, so manual overrides are never
  undone by stale events, and unknown values stay null; plus **MIGRATE-01** compatibility fixtures
  (neutral, not a Plex importer).
- **DATA-3** (Plex import) is G4 and optional. The never-downgrade/dry-run design in T12–T15 is a
  good basis, but it is not on the G1 path. Its job manager is in memory only. Spec §10.2 needs
  persisted job intent and status with idempotent retry, reconciled on restart.
- **LIB-1 scope (fixed 2026-10-27):** this map first gave LIB-1 the draft's probe and name/NFO tasks (T1, T2, T8–T10). The spec's LIB-1 row is discovery, stable-file checks, bounded probe jobs and source revisions (§3.6), Track B. LIB-1's own plan now covers that; name/NFO parsing moved to LIB-2 (META-1 may take NFO IDs).
