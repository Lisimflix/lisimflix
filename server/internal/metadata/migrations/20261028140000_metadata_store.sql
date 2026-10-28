-- Metadata (META-1): matched metadata per catalogue item, with provenance
-- and locks per field (CTR-2's ItemMetadata), provider IDs, the review
-- queue's candidates and each provider's pause state. Times are Unix
-- milliseconds (UTC). Rows exist only for items META-1 has queued; an item
-- without one shows its parsed name (LIB-2a) and is "not matched yet".

CREATE TABLE metadata_items (
    item_id         TEXT PRIMARY KEY REFERENCES library_items(id) ON DELETE CASCADE,
    revision        INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    match_status    TEXT NOT NULL DEFAULT 'unmatched'
                    CHECK (match_status IN ('unmatched', 'matched', 'review', 'conflict')),
    next_attempt_at INTEGER,          -- due for matching; NULL when not queued
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT,             -- why the last attempt failed (bounded), for the admin
    updated_at      INTEGER NOT NULL
) STRICT;
CREATE INDEX metadata_items_due ON metadata_items(next_attempt_at) WHERE next_attempt_at IS NOT NULL;

-- One row per field that has a value or a lock. value is JSON: a string,
-- an integer, {"region","value"} or a list of strings, as CTR-2's fields.
CREATE TABLE metadata_fields (
    item_id     TEXT NOT NULL REFERENCES metadata_items(item_id) ON DELETE CASCADE,
    field       TEXT NOT NULL CHECK (field IN ('title', 'sortTitle', 'originalTitle', 'overview',
                                               'year', 'contentRating', 'genres')),
    value       TEXT,                 -- JSON; NULL = no value
    provider    TEXT,                 -- provenance: tmdb, nfo, filename, user ...; NULL = unknown
    provider_id TEXT,
    fetched_at  INTEGER,
    locked      INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
    PRIMARY KEY (item_id, field)
) STRICT;

-- The item's identity at each provider. A locked ID (a user's fix-match)
-- is never replaced by a match; a different hint raises a conflict instead
-- (spec §3.7).
CREATE TABLE metadata_external_ids (
    item_id     TEXT NOT NULL REFERENCES metadata_items(item_id) ON DELETE CASCADE,
    namespace   TEXT NOT NULL,
    value       TEXT NOT NULL,
    provider    TEXT,
    provider_id TEXT,
    fetched_at  INTEGER,
    locked      INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
    PRIMARY KEY (item_id, namespace)
) STRICT;

-- The review queue: candidates for items no candidate matched confidently.
CREATE TABLE metadata_candidates (
    item_id     TEXT NOT NULL REFERENCES metadata_items(item_id) ON DELETE CASCADE,
    provider    TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    title       TEXT NOT NULL,
    year        INTEGER,
    score       REAL NOT NULL,
    PRIMARY KEY (item_id, provider, provider_id)
) STRICT;

-- A provider paused by a configuration or authentication error (a bad or
-- revoked key) until the admin resumes it (spec §3.7).
CREATE TABLE metadata_providers (
    name          TEXT PRIMARY KEY,
    paused_reason TEXT,
    paused_at     INTEGER
) STRICT;
