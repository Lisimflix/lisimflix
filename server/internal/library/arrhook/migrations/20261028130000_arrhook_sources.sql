-- Import notifications (LIB-4): Sonarr and Radarr instances allowed to queue
-- scans, the library folders each may touch, how their paths map onto the
-- media root, and the last receipts for the admin view. Times are Unix
-- milliseconds (UTC).

CREATE TABLE arrhook_sources (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL CHECK (kind IN ('sonarr', 'radarr')),
    token_hash BLOB NOT NULL,          -- SHA-256 of the token; the token itself is shown once
    created_at INTEGER NOT NULL,
    revoked_at INTEGER                 -- set: every request is refused
) STRICT;

-- The library folders a source may queue scans in.
CREATE TABLE arrhook_roots (
    source_id TEXT NOT NULL REFERENCES arrhook_sources(id) ON DELETE CASCADE,
    root_id   TEXT NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    PRIMARY KEY (source_id, root_id)
) STRICT;

-- remote: a folder as the source reports it ("/tv"); local: the same folder
-- relative to the media root ("TV"). Paths are mapped by the longest match.
CREATE TABLE arrhook_mappings (
    source_id TEXT NOT NULL REFERENCES arrhook_sources(id) ON DELETE CASCADE,
    remote    TEXT NOT NULL,
    local     TEXT NOT NULL,
    PRIMARY KEY (source_id, remote)
) STRICT;

-- What each notification did. A receipt means queued, not scanned: the scan
-- runs (cause 'import') record the outcome.
CREATE TABLE arrhook_receipts (
    id          TEXT PRIMARY KEY,
    source_id   TEXT NOT NULL REFERENCES arrhook_sources(id) ON DELETE CASCADE,
    received_at INTEGER NOT NULL,
    event       TEXT NOT NULL,         -- eventType as sent, e.g. Download
    app         TEXT NOT NULL,         -- from User-Agent, e.g. Sonarr/4.0.20.3014
    result      TEXT NOT NULL CHECK (result IN ('queued', 'test', 'ignored', 'rejected')),
    reason      TEXT,                  -- why it was rejected or ignored
    scopes      TEXT NOT NULL DEFAULT '[]', -- JSON [{"rootId","path"}] queued
    request_id  TEXT
) STRICT;
CREATE INDEX arrhook_receipts_source ON arrhook_receipts(source_id, received_at);
