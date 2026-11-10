-- Personal progress (DATA-1a): per-profile state for one logical item, the
-- merged playback coverage per source, and the progress event log. Times are
-- Unix milliseconds (UTC); positions and durations are milliseconds. A NULL
-- column means unknown, never 0 or false. No foreign keys: profiles (AUTH-3)
-- and catalogue items live in other modules.

-- A row exists after the first change of any kind. A row with progress only
-- has revision 0, the same as no row.
CREATE TABLE userdata_state (
    profile_id             TEXT NOT NULL,
    item_id                TEXT NOT NULL,
    revision               INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    resume_position_ms     INTEGER,
    resume_duration_ms     INTEGER,
    resume_version_id      TEXT,
    resume_source_revision TEXT,
    resume_updated_at      INTEGER,
    watched_value          INTEGER,
    watched_origin         TEXT,
    watched_at             INTEGER,
    play_count             INTEGER,
    last_played_at         INTEGER,
    hidden                 INTEGER NOT NULL DEFAULT 0,
    hide_event_id          INTEGER,
    reset_event_id         INTEGER,
    covered_ms             INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (profile_id, item_id)
) STRICT;
-- Continue Watching.
CREATE INDEX userdata_state_recent ON userdata_state(profile_id, last_played_at DESC);

-- The merged coverage per source.
CREATE TABLE userdata_intervals (
    profile_id      TEXT NOT NULL,
    item_id         TEXT NOT NULL,
    version_id      TEXT NOT NULL,
    source_revision TEXT NOT NULL,
    start_ms        INTEGER NOT NULL,
    end_ms          INTEGER NOT NULL,
    PRIMARY KEY (profile_id, item_id, version_id, source_revision, start_ms)
) STRICT;

CREATE TABLE userdata_events (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    profile_id       TEXT NOT NULL,
    item_id          TEXT NOT NULL,
    session_id       TEXT NOT NULL,
    sequence         INTEGER NOT NULL,
    kind             TEXT NOT NULL,
    position_ms      INTEGER NOT NULL,
    from_position_ms INTEGER,
    version_id       TEXT NOT NULL,
    source_revision  TEXT NOT NULL,
    accepted         INTEGER NOT NULL,
    reason           TEXT,
    received_at      INTEGER NOT NULL
) STRICT;
-- Partial on purpose: a replayed event is stored as a rejected row with the
-- same key, so only accepted rows may be unique.
CREATE UNIQUE INDEX userdata_events_accepted ON userdata_events(profile_id, session_id, sequence) WHERE accepted = 1;
-- A session's lowest event id (the lateness lookup) is one seek; the partial
-- index above can't serve it because it only covers accepted rows.
CREATE INDEX userdata_events_session ON userdata_events(profile_id, session_id, id);
