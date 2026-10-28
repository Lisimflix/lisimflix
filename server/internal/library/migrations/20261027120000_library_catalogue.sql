-- Library catalogue (LIB-2): libraries, their roots, scan runs, the files
-- found under each root, and the items and versions they make up. Times are
-- Unix milliseconds (UTC); *_ns columns are file modification times in
-- nanoseconds, as LIB-1's Revision holds them.

CREATE TABLE library_libraries (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('movies', 'tv', 'anime')),
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE library_roots (
    id         TEXT PRIMARY KEY,
    library_id TEXT NOT NULL REFERENCES library_libraries(id) ON DELETE CASCADE,
    path       TEXT NOT NULL UNIQUE, -- slash-separated, relative to the media root
    state      TEXT NOT NULL DEFAULT 'available'
               CHECK (state IN ('available', 'scanning', 'degraded', 'offline')),
    reason     TEXT,                 -- why the last scan failed; NULL after a success
    last_ok_at INTEGER               -- when the last successful scan finished
) STRICT;

CREATE TABLE library_scan_runs (
    id          TEXT PRIMARY KEY,
    root_id     TEXT NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER,
    status      TEXT NOT NULL
                CHECK (status IN ('running', 'completed', 'failed', 'cancelled', 'interrupted')),
    reason      TEXT,
    seen        INTEGER NOT NULL DEFAULT 0,
    added       INTEGER NOT NULL DEFAULT 0,
    changed     INTEGER NOT NULL DEFAULT 0,
    returned    INTEGER NOT NULL DEFAULT 0,
    missing     INTEGER NOT NULL DEFAULT 0,
    unplaced    INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX library_scan_runs_root ON library_scan_runs(root_id, status, started_at);

CREATE TABLE library_files (
    id             TEXT PRIMARY KEY,
    root_id        TEXT NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    path           TEXT NOT NULL,    -- slash-separated, relative to the root
    size           INTEGER NOT NULL,
    mtime_ns       INTEGER NOT NULL,
    state          TEXT NOT NULL DEFAULT 'present' CHECK (state IN ('present', 'missing', 'retired')),
    missing_since  INTEGER,          -- first successful scan that didn't find it
    last_seen_run  TEXT,
    unplaced       TEXT,             -- why naming couldn't place it: unrecognised, extra, sample
    probe_size     INTEGER,          -- the revision the probe columns describe
    probe_mtime_ns INTEGER,
    probe_json     TEXT,
    probe_error    TEXT,
    probe_after    INTEGER,          -- not before this time: quiet period after a change (#62)
    UNIQUE (root_id, path)
) STRICT;

CREATE TABLE library_items (
    id         TEXT PRIMARY KEY,
    library_id TEXT NOT NULL REFERENCES library_libraries(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('movie', 'show', 'season', 'episode')),
    parent_id  TEXT REFERENCES library_items(id) ON DELETE CASCADE,
    scan_key   TEXT NOT NULL,        -- identity from names; see naming.Entry.Key
    title      TEXT NOT NULL,
    sort_title TEXT NOT NULL,
    year       INTEGER,
    idx        INTEGER,              -- season or episode number
    air_date   TEXT,                 -- daily episodes, YYYY-MM-DD
    absolute   INTEGER,              -- anime absolute number, for META-2
    created_at INTEGER NOT NULL,
    UNIQUE (library_id, scan_key)
) STRICT;
CREATE INDEX library_items_parent ON library_items(parent_id);

CREATE TABLE library_versions (
    id      TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE,
    file_id TEXT NOT NULL REFERENCES library_files(id) ON DELETE CASCADE,
    label   TEXT,
    UNIQUE (item_id, file_id)
) STRICT;
CREATE INDEX library_versions_file ON library_versions(file_id);

-- Provider ID hints from file and folder names. Two different values for one
-- namespace are kept, so META-1 can flag the conflict instead of picking one.
CREATE TABLE library_id_hints (
    item_id   TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE,
    namespace TEXT NOT NULL,
    value     TEXT NOT NULL,
    PRIMARY KEY (item_id, namespace, value)
) STRICT;

-- Which movie or show each naming folder belongs to (Entry.Folder; '' for
-- loose files in the root). Once a folder is mapped, its files always go to
-- that item, so a decision about a possible duplicate sticks.
CREATE TABLE library_folders (
    root_id  TEXT NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    folder   TEXT NOT NULL,
    scan_key TEXT NOT NULL,          -- the naming key the folder produced
    item_id  TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE,
    PRIMARY KEY (root_id, folder, scan_key)
) STRICT;

-- A folder whose name matches an existing item from another folder gets its
-- own provisional item (other_id). After a completed scan, a pair whose
-- existing item has no present files was a rename or move and is folded
-- together; otherwise both folders exist and the pair is flagged for the
-- admin to merge or keep separate. Nothing merges two coexisting folders
-- automatically.
CREATE TABLE library_duplicates (
    id       TEXT PRIMARY KEY,
    root_id  TEXT NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE, -- the other folder's root
    item_id  TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE, -- the existing item
    other_id TEXT NOT NULL REFERENCES library_items(id) ON DELETE CASCADE, -- the provisional item
    state    TEXT NOT NULL CHECK (state IN ('pending', 'flagged', 'separate')),
    UNIQUE (item_id, other_id)
) STRICT;
